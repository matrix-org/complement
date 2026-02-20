package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	set "github.com/deckarep/golang-set"
	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// SJ: Send Join tests (/send_join)
//  SJ00: on malformed send_join response JSON, refuse to join.
//    A: missing state_dag field
//    B: state_dag field is not an array
//    C: state_dag elements are not JSON objects
//  SJ01(IO): if the state DAG is valid and connected, accept.
//  SJ02: invalid state DAG => refuse to join. Returns an event in state_dag which is rejected because it:
//    A: is missing all prev_state_events
//    B: is missing partial prev_state_events
//    C: is referencing only message events in prev_state_events
//    D: is referencing message events in addition to correct events in prev_state_events
//    E: is failing auth checks at prev_state_events i.e it is a rejected event e.g user is not joined
//    F: has 0 prev_state_events but it isn't the m.room.create event
//    G: has >0 prev_state_events and it is an m.room.create event
//    H: is a valid event that references a rejected event as a causal predecessor.
//  SJ03: faster remote room joins
//    A(IO): if set, returns partial_state_event_ids and timeline fields.
//    B: The timeline field honours history visibility.
//    C: The partial_state_event_ids includes non-member state and is the current state not historical.
//    D: partial_auth_chain_ids cycles are broken
//  SJ04: When returning the state DAG to new joiners
//    A: exclude rejected events
//    B: exclude malformed events
//    C: include concurrent events which aren't part of the current state (i.e a form of soft failure)

func TestMSC4242SendJoinMalformedResponseSJ00(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		// accept incoming presence transactions, etc
		federation.HandleTransactionRequests(nil, nil),
		// accept incoming /event requests
		federation.HandleEventRequests(),
	)
	cancel := srv.Listen()
	defer cancel()
	bob := srv.UserID("bob")
	srv.Mux().Handle("/_matrix/federation/v1/make_join/{roomID}/{userID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		federation.MakeJoinRequestsHandler(srv, w, req)
	})).Methods("GET")

	var mutate func(resp []byte) ([]byte, error)
	var mu sync.Mutex
	srv.Mux().Handle("/_matrix/federation/v2/send_join/{roomID}/{eventID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fakeWriter := httptest.NewRecorder()
		federation.SendJoinRequestsHandler(srv, fakeWriter, req, false, false)
		mu.Lock()
		respJSON, err := mutate(fakeWriter.Body.Bytes())
		mu.Unlock()
		must.NotError(t, "failed to delete bytes", err)
		w.WriteHeader(fakeWriter.Code)
		w.Write(respJSON)
		t.Logf("sending back: %s", string(respJSON))
	})).Methods("PUT")

	testCases := []struct {
		testCode string
		name     string
		mutate   func(resp []byte) ([]byte, error)
	}{
		{
			testCode: "SJ00A",
			name:     "Missing state_dag field",
			mutate: func(resp []byte) ([]byte, error) {
				return sjson.DeleteBytes(resp, "state_dag")
			},
		},
		{
			testCode: "SJ00B",
			name:     "state_dag field is not an array",
			mutate: func(resp []byte) ([]byte, error) {
				return sjson.SetBytes(resp, "state_dag", 42)
			},
		},
		{
			testCode: "SJ00C",
			name:     "state_dag field elements are not JSON objects",
			mutate: func(resp []byte) ([]byte, error) {
				return sjson.SetBytes(resp, "state_dag", []string{"alpha", "bet", "soup"})
			},
		},
	}
	for _, tc := range testCases {
		room := srv.MustMakeRoom(
			t, roomVersion,
			federation.InitialRoomEvents(roomVersion, bob),
			federation.WithImpl(ServerRoomImplStateDAG(t, srv)),
		)
		mu.Lock()
		mutate = tc.mutate
		mu.Unlock()

		res := alice.JoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 502,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_UNKNOWN"),
			},
		})
	}
}

func TestMSC4242SendJoinSJ01Outbound(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		// accept incoming presence transactions, etc
		federation.HandleTransactionRequests(nil, nil),
		// accept incoming /event requests
		federation.HandleEventRequests(),
		federation.HandleMakeSendJoinRequests(),
	)
	cancel := srv.Listen()
	defer cancel()
	bob := srv.UserID("bob")
	room := srv.MustMakeRoom(t, roomVersion,
		federation.InitialRoomEvents(roomVersion, bob),
		federation.WithImpl(ServerRoomImplStateDAG(t, srv)),
	)
	alice.MustJoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})
}

func TestMSC4242SendJoinSJ01Inbound(t *testing.T) {
	roomVer := gomatrixserverlib.MustGetRoomVersion(roomVersion)
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		// accept incoming presence transactions, etc
		federation.HandleTransactionRequests(nil, nil),
		// accept incoming /event requests
		federation.HandleEventRequests(),
	)
	cancel := srv.Listen()
	defer cancel()

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"room_version": roomVersion,
		"preset":       "public_chat",
	})
	changeDisplayName(t, alice, "alice", 5)
	sendTextMessages(t, alice, roomID, "before_join", 5)
	roomSyncData, _ := alice.MustSync(t, client.SyncReq{
		Filter: `{"event_format":"federation","room":{"timeline":{"limit":50}}}`,
	})

	// the join event should have the same prev_state_events as the last message in the room
	lastEvent := roomSyncData.Get("rooms.join." + client.GjsonEscape(roomID) + ".timeline.events.@reverse.0")
	must.Equal(t, lastEvent.Exists(), true, "last timeline entry for room does not exist")
	expectedJoinprevStateEvents := prevStateEvents(t, lastEvent)
	bob := srv.UserID("bob")
	_, sendJoinResp := MustJoinRoom(t, srv, deployment, spec.ServerName("hs1"), roomID, bob)
	joinEvent, err := roomVer.NewEventFromTrustedJSON(sendJoinResp.Event, false)
	must.NotError(t, "failed to load join event", err)
	stateDAG := make(map[string][]string) // event_id => prev_state_events
	// Specifically tests that:
	//  - the state DAG is returned and is connected
	//  - the state dag for all room state is included
	//  - there are no events under the 'state'or 'auth_chain' keys (only 'state_dag')
	must.MatchJSONBytes(t, sendJoinResp.Event,
		match.JSONKeyMissing("auth_events"),
		match.JSONKeyPresent("prev_state_events"),
		prevStateEventsMatcher("prev_state_events", expectedJoinprevStateEvents),
	)
	must.Equal(t, len(sendJoinResp.StateEvents), 0, "/send_join response included events under 'state'")
	must.Equal(t, len(sendJoinResp.AuthEvents), 0, "/send_join response included events under 'auth_chain'")
	must.Equal(t, len(sendJoinResp.ServersInRoom), 0, "/send_join response included events under 'servers_in_room'")

	wantStateEventTypes := set.NewSetFromSlice(
		[]interface{}{
			[2]string{"m.room.create", ""},
			[2]string{spec.MRoomMember, alice.UserID},
			// NB: Bob is NOT part of this as his join is the 'Event' in the send_join response
			[2]string{"m.room.power_levels", ""},
			[2]string{"m.room.join_rules", ""},
			[2]string{"m.room.history_visibility", ""},
		},
	)

	// ensure the state DAG is connected: that means we do not have any prev_state_events
	// that we haven't been told about.
	knownEventIDs := set.NewSet()
	allKnownPrevStateEvents := set.NewSet()
	for _, stateEventJSON := range sendJoinResp.StateDAG {
		stateEvent, err := roomVer.NewEventFromTrustedJSON(stateEventJSON, false)
		must.NotError(t, "failed to load event json", err)
		must.NotEqual(t, stateEvent.StateKey(), nil, fmt.Sprintf("state_dag returned a non-state event: %s", string(stateEventJSON)))
		key := [2]string{stateEvent.Type(), *stateEvent.StateKey()}
		wantStateEventTypes.Remove(key)
		stateEventGJSON := gjson.ParseBytes(stateEventJSON)
		prevStateEvents := prevStateEvents(t, stateEventGJSON)
		for _, pae := range prevStateEvents {
			allKnownPrevStateEvents.Add(pae)
		}
		t.Logf("Event ID %s", stateEvent.EventID())
		t.Logf("%s", string(stateEvent.JSON()))
		knownEventIDs.Add(stateEvent.EventID())
		stateDAG[stateEvent.EventID()] = prevStateEvents
	}

	must.Equal(t, wantStateEventTypes.Cardinality(), 0, fmt.Sprintf("did not see all room state: missing %v", wantStateEventTypes.String()))

	// Include the prev state events of the join event itself as that is what we care about
	joinEventprevStateEvents := prevStateEvents(t, gjson.ParseBytes(sendJoinResp.Event))
	for _, pae := range joinEventprevStateEvents {
		allKnownPrevStateEvents.Add(pae)
	}
	stateDAG[joinEvent.EventID()] = joinEventprevStateEvents

	t.Logf("known event IDs: %v", knownEventIDs.String())
	t.Logf("all known prev state events: %v", allKnownPrevStateEvents.String())
	// we expect 10 known prev state events:
	// - the initial set from room creation (create,join,pl,join_rules, his vis)
	// - 5x profile changes
	must.Equal(t, allKnownPrevStateEvents.Cardinality(), 10, "unexpected number of unique prev_state_events")
	// these 9 must be all present in the known event IDs, i.e it's a subset.
	// We may return more but we don't have to, hence it isn't guaranteed to be a proper subset.
	must.Equal(t, allKnownPrevStateEvents.IsSubset(knownEventIDs), true, "returned state_dag is not connected")
}

func TestMSC4242SendJoinSJ02InvalidStateDAG(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		// accept incoming presence transactions, etc
		federation.HandleTransactionRequests(nil, nil),
		// accept incoming /event requests
		federation.HandleEventRequests(),
		federation.HandleMakeSendJoinRequests(),
	)
	cancel := srv.Listen()
	defer cancel()
	bob := srv.UserID("bob")
	testCases := faultyEventTestCases
	var mu sync.Mutex
	for _, tc := range testCases {
		t.Logf("SJ02%s: %s", tc.CodeSuffix, tc.Name)
		var faultyEvents []gomatrixserverlib.PDU
		room := srv.MustMakeRoom(t, roomVersion,
			federation.InitialRoomEvents(roomVersion, bob),
			federation.WithImpl(ServerRoomImplStateDAG(t, srv, WithMutateSendJoinResponse(func(resp *fclient.RespSendJoin, joinEvent gomatrixserverlib.PDU) {
				mu.Lock()
				defer mu.Unlock()
				if len(faultyEvents) == 0 {
					panic("programming error: no faulty events have been provided")
				}
				// the state events will be included already, so we just need to add in non-state events
				for _, faultyEvent := range faultyEvents {
					if faultyEvent.StateKey() != nil {
						continue
					}
					resp.StateDAG = append(resp.StateDAG, faultyEvent.JSON())
				}
			}))),
		)
		mu.Lock()
		faultyEvents = tc.GenerateEvents(t, srv, room, bob)
		mu.Unlock()
		for _, ev := range faultyEvents {
			room.AddEvent(ev)
		}
		res := alice.JoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 502,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_UNKNOWN"),
			},
		})
	}
}

/*
func TestMSC4242SendJoinFasterSJ03Inbound(t *testing.T) {
	roomVer := gomatrixserverlib.MustGetRoomVersion(roomVersion)
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		// accept incoming presence transactions, etc
		federation.HandleTransactionRequests(nil, nil),
		// accept incoming /event requests
		federation.HandleEventRequests(),
	)
	cancel := srv.Listen()
	defer cancel()

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	charlie := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"room_version": roomVersion,
		"preset":       "public_chat",
	})
	charlie.MustJoinRoom(t, roomID, []string{"hs1"})
	changeDisplayName(t, alice, "alice", 4)
	changeDisplayName(t, alice, "final", 1)
	textEventIDs := sendTextMessages(t, alice, roomID, "before_join", 5)
	t.Logf("sent messages: %v", textEventIDs)
	// we must sync with LL members enabled.
	roomSyncData, _ := alice.MustSync(t, client.SyncReq{
		Filter: `{"event_format":"federation","room":{"state":{"lazy_load_members":true},"timeline":{"lazy_load_members":true,"limit":50}}}`,
	})

	// the join event should have the same prev_state_events as the last message in the room
	lastEvent := roomSyncData.Get("rooms.join." + client.GjsonEscape(roomID) + ".timeline.events.@reverse.0")
	must.Equal(t, lastEvent.Exists(), true, "last timeline entry for room does not exist")
	expectedJoinprevStateEvents := prevStateEvents(t, lastEvent)
	bob := srv.UserID("bob")
	timelineLimit := 4
	sendJoinResp := mustJoinPartialRoom(t, srv, deployment, spec.ServerName("hs1"), roomID, bob, timelineLimit)
	x, _ := json.Marshal(sendJoinResp)
	t.Logf("/send_join response: %v", string(x))
	must.Equal(t, sendJoinResp.MembersOmitted, true, "members_omitted was not true")
	joinEvent, err := roomVer.NewEventFromTrustedJSON(sendJoinResp.Event, false)
	must.NotError(t, "failed to load join event", err)
	stateDAG := make(map[string][]string) // event_id => prev_state_events
	// Specifically tests that:
	//  - the state DAG is returned and is connected
	//  - the state dag for all room state is included
	//  - there are no events under the 'state'or 'auth_chain' keys (only 'state_dag')
	must.MatchJSONBytes(t, sendJoinResp.Event,
		match.JSONKeyMissing("auth_events"),
		match.JSONKeyPresent("prev_state_events"),
		prevStateEventsMatcher("prev_state_events", expectedJoinprevStateEvents),
	)
	must.Equal(t, len(sendJoinResp.StateEvents), 0, "/send_join response included events under 'state'")
	must.Equal(t, len(sendJoinResp.AuthEvents), 0, "/send_join response included events under 'auth_chain'")

	wantStateEventTypes := set.NewSetFromSlice(
		[]interface{}{
			[2]string{spec.MRoomCreate, ""},
			[2]string{spec.MRoomMember, alice.UserID},
			[2]string{spec.MRoomMember, charlie.UserID},
			// NB: Bob is NOT part of this as his join is the 'Event' in the send_join response
			[2]string{spec.MRoomPowerLevels, ""},
			[2]string{spec.MRoomJoinRules, ""},
			[2]string{spec.MRoomHistoryVisibility, ""},
		},
	)

	// ensure the state DAG is connected: that means we do not have any prev_state_events
	// that we haven't been told about.
	knownEventIDs := set.NewSet()
	allKnownPrevStateEvents := set.NewSet()
	currentState := make(map[[2]string]string) // (type, skey) => event ID
	var authChainAliceIDs []string
	for _, stateEventJSON := range sendJoinResp.StateDAG {
		stateEvent, err := roomVer.NewEventFromTrustedJSON(stateEventJSON, false)
		must.NotError(t, "failed to load event json", err)
		must.NotEqual(t, stateEvent.StateKey(), nil, fmt.Sprintf("state_dag returned a non-state event: %s", string(stateEventJSON)))
		key := [2]string{stateEvent.Type(), *stateEvent.StateKey()}
		wantStateEventTypes.Remove(key)
		stateEventGJSON := gjson.ParseBytes(stateEventJSON)
		prevStateEvents := prevStateEvents(t, stateEventGJSON)
		for _, pae := range prevStateEvents {
			allKnownPrevStateEvents.Add(pae)
		}
		t.Logf("Event ID %s  (%s,%s)", stateEvent.EventID(), stateEvent.Type(), *stateEvent.StateKey())
		t.Logf("%s", string(stateEvent.JSON()))
		knownEventIDs.Add(stateEvent.EventID())
		stateDAG[stateEvent.EventID()] = prevStateEvents
		if stateEvent.Type() == spec.MRoomMember && *stateEvent.StateKey() == alice.UserID {
			// we only want the event ID which is the last one, which conveniently has the display name "final" in it.
			if !strings.HasPrefix(gjson.GetBytes(stateEvent.Content(), "displayname").Str, "final") {
				authChainAliceIDs = append(authChainAliceIDs, stateEvent.EventID())
				continue
			}
		}
		currentState[[2]string{stateEvent.Type(), *stateEvent.StateKey()}] = stateEvent.EventID()
	}
	must.Equal(t, wantStateEventTypes.Cardinality(), 0, fmt.Sprintf("did not see all room state: missing %v", wantStateEventTypes.String()))
	// Include the prev state events of the join event itself as that is what we care about
	joinEventPrevStateEvents := prevStateEvents(t, gjson.ParseBytes(sendJoinResp.Event))
	for _, pae := range joinEventPrevStateEvents {
		allKnownPrevStateEvents.Add(pae)
	}
	stateDAG[joinEvent.EventID()] = joinEventPrevStateEvents
	t.Logf("known event IDs: %v", knownEventIDs.String())
	t.Logf("all known prev state events: %v", allKnownPrevStateEvents.String())
	// we expect 11 known prev state events:
	// - the initial set from room creation (create,join,pl,join_rules, his vis)
	// - charlie's join
	// - 5x profile changes
	must.Equal(t, allKnownPrevStateEvents.Cardinality(), 11, "unexpected number of unique prev_state_events")
	// these 9 must be all present in the known event IDs, i.e it's a subset.
	// We may return more but we don't have to, hence it isn't guaranteed to be a proper subset.
	must.Equal(t, allKnownPrevStateEvents.IsSubset(knownEventIDs), true, "returned state_dag is not connected")

	// ============================

	// Now check the FRRJ timeline
	timeline := sendJoinResp.Timeline.UntrustedEvents(roomVersion)
	if len(timeline) != timelineLimit {
		ct.Fatalf(t, "unexpected timeline length: got %d want %d", len(timeline), timelineLimit)
	}
	gotTimelineEvents := timeline[len(timeline)-timelineLimit:]
	gotTimelineEventIDs := make([]string, len(gotTimelineEvents))
	for i := range gotTimelineEvents {
		gotTimelineEventIDs[i] = gotTimelineEvents[i].EventID()
	}
	wantTimelineEventIDs := textEventIDs[len(textEventIDs)-timelineLimit:]
	if !reflect.DeepEqual(gotTimelineEventIDs, wantTimelineEventIDs) {
		ct.Fatalf(t, "unexpected timeline, got %v want %v", gotTimelineEventIDs, wantTimelineEventIDs)
	}

	// And the FRRJ partial state event IDs:
	//  - No charlie (omit members)
	//  - No old member events
	wantPartialStateIDs := []string{
		currentState[[2]string{spec.MRoomCreate, ""}],
		currentState[[2]string{spec.MRoomJoinRules, ""}],
		currentState[[2]string{spec.MRoomPowerLevels, ""}],
		currentState[[2]string{spec.MRoomHistoryVisibility, ""}],
		currentState[[2]string{spec.MRoomMember, alice.UserID}],
	}
	slices.Sort(wantPartialStateIDs)
	slices.Sort(sendJoinResp.PartialStateEventIDs)
	if !reflect.DeepEqual(wantPartialStateIDs, sendJoinResp.PartialStateEventIDs) {
		ct.Fatalf(t, "wrong partial state IDs:\vgot  %v\nwant %v\n", sendJoinResp.PartialStateEventIDs, wantPartialStateIDs)
	}
	// And the FRRJ partial auth chain IDs:
	// - No charlie (omit members)
	// - with old membership events (auth chain for alice)
	wantPartialAuthStateIDs := append([]string{
		currentState[[2]string{spec.MRoomCreate, ""}],
		currentState[[2]string{spec.MRoomPowerLevels, ""}],
		currentState[[2]string{spec.MRoomJoinRules, ""}],
	}, authChainAliceIDs...)
	slices.Sort(wantPartialAuthStateIDs)
	var gotPartialAuthStateIDs []string
	for k := range sendJoinResp.PartialAuthChainIDs {
		gotPartialAuthStateIDs = append(gotPartialAuthStateIDs, k)
	}
	slices.Sort(gotPartialAuthStateIDs)
	if !reflect.DeepEqual(wantPartialAuthStateIDs, gotPartialAuthStateIDs) {
		ct.Fatalf(t, "wrong partial state auth chain IDs:\vgot  %v\nwant %v\n", gotPartialAuthStateIDs, wantPartialAuthStateIDs)
	}
}

func TestMSC4242SendJoinFasterSJ03Outbound(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		// accept incoming presence transactions, etc
		federation.HandleTransactionRequests(nil, nil),
		// accept incoming /event requests
		federation.HandleEventRequests(),
		federation.HandleMakeSendJoinRequests(),
	)
	cancel := srv.Listen()
	defer cancel()
	bob := srv.UserID("bob")
	var mutateSendJoinResponse func(resp *fclient.RespSendJoin, joinEvent gomatrixserverlib.PDU)
	room := srv.MustMakeRoom(t, roomVersion,
		federation.InitialRoomEvents(roomVersion, bob),
		federation.WithImpl(ServerRoomImplStateDAG(t, srv, WithMutateSendJoinResponse(func(resp *fclient.RespSendJoin, joinEvent gomatrixserverlib.PDU) {
			mutateSendJoinResponse(resp, joinEvent)
		}))),
	)
	// skip all member events
	mutateSendJoinResponse = func(resp *fclient.RespSendJoin, joinEvent gomatrixserverlib.PDU) {
		resp.MembersOmitted = true
		resp.PartialAuthChainIDs = make(map[string][]string)
		resp.PartialAuthChainIDs[joinEvent.EventID()] = []string{
			room.CurrentState(spec.MRoomCreate, "").EventID(),
			room.CurrentState(spec.MRoomPowerLevels, "").EventID(),
			room.CurrentState(spec.MRoomJoinRules, "").EventID(),
		}
		for _, ev := range room.AllCurrentState() {
			if ev.Type() == spec.MRoomMember {
				continue
			}
			resp.PartialStateEventIDs = append(resp.PartialStateEventIDs, ev.EventID())
			// set default auth events from initial room creation events.
			switch ev.Type() {
			case spec.MRoomCreate:
				resp.PartialAuthChainIDs[ev.EventID()] = []string{}
			case spec.MRoomMember:
				resp.PartialAuthChainIDs[ev.EventID()] = []string{
					room.CurrentState(spec.MRoomCreate, "").EventID(),
				}
			case spec.MRoomPowerLevels:
				resp.PartialAuthChainIDs[ev.EventID()] = []string{
					room.CurrentState(spec.MRoomCreate, "").EventID(),
					room.CurrentState(spec.MRoomMember, bob).EventID(),
				}
			default:
				resp.PartialAuthChainIDs[ev.EventID()] = []string{
					room.CurrentState(spec.MRoomCreate, "").EventID(),
					room.CurrentState(spec.MRoomMember, string(ev.SenderID())).EventID(),
					room.CurrentState(spec.MRoomPowerLevels, "").EventID(),
				}
			}
		}
		fmt.Printf("PartialAuthChainIDs: %+v\n", resp.PartialAuthChainIDs)
	}
	alice.MustJoinRoom(t, room.RoomID, []string{srv.ServerName()})
} */

func TestMSC4242OnSendJoinSJ04(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		// accept incoming presence transactions, etc
		federation.HandleTransactionRequests(nil, nil),
		// accept incoming /event requests
		federation.HandleEventRequests(),
		federation.HandleInviteRequests(nil),
	)
	srv.UnexpectedRequestsAreErrors = false // /profile queries when doing invites
	cancel := srv.Listen()
	defer cancel()
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := srv.UserID("bob")
	charlie := srv.UserID("charlie")

	testCases := []struct {
		testCode                          string
		name                              string
		sendEvents                        func(t *testing.T, room *federation.ServerRoom) []gomatrixserverlib.PDU
		assertEventsAreInSendJoinResponse bool
	}{
		{
			testCode:                          "SJ04A",
			name:                              "Rejected events are not sent in /send_join responses",
			assertEventsAreInSendJoinResponse: false,
			sendEvents: func(t *testing.T, room *federation.ServerRoom) []gomatrixserverlib.PDU {
				return []gomatrixserverlib.PDU{
					// Charlie cannot join the room as it is invite-only
					mustCreateEvent(t, srv, room, MSC4242Event{
						Event: federation.Event{
							Type:     spec.MRoomMember,
							Sender:   charlie,
							StateKey: &charlie,
							Content: map[string]interface{}{
								"membership": spec.Join,
							},
						},
						PrevStateEvents: []string{
							room.CurrentState(spec.MRoomMember, bob).EventID(),
						},
					}),
				}
			},
		},
		{
			testCode:                          "SJ04B",
			name:                              "Malformed events are not sent in /send_join responses",
			assertEventsAreInSendJoinResponse: false,
			sendEvents: func(t *testing.T, room *federation.ServerRoom) []gomatrixserverlib.PDU {
				return []gomatrixserverlib.PDU{
					mustCreateEvent(t, srv, room, MSC4242Event{
						Event: federation.Event{
							Type:     spec.MRoomName,
							Sender:   bob,
							StateKey: &empty,
							Content: map[string]interface{}{
								"name": "I have no prev_state_events",
							},
						},
						PrevStateEvents: []string{}, // no prev_state_events
					}),
				}
			},
		},
		{
			testCode:                          "SJ04C",
			name:                              "Concurrent state events which aren't part of the current state are sent in /send_join responses",
			assertEventsAreInSendJoinResponse: true,
			sendEvents: func(t *testing.T, room *federation.ServerRoom) []gomatrixserverlib.PDU {
				charlieInvite := mustCreateEvent(t, srv, room, MSC4242Event{
					Event: federation.Event{
						Type:     spec.MRoomMember,
						Sender:   bob,
						StateKey: &charlie,
						Content: map[string]interface{}{
							"membership": spec.Invite,
						},
					},
					PrevStateEvents: []string{
						room.CurrentState(spec.MRoomMember, bob).EventID(),
					},
				})
				room.AddEvent(charlieInvite)
				charlieJoin := mustCreateEvent(t, srv, room, MSC4242Event{
					Event: federation.Event{
						Type:     spec.MRoomMember,
						Sender:   charlie,
						StateKey: &charlie,
						Content: map[string]interface{}{
							"membership": spec.Join,
						},
					},
					PrevStateEvents: []string{
						charlieInvite.EventID(),
					},
				})
				room.AddEvent(charlieJoin)
				charlieMod := mustCreateEvent(t, srv, room, MSC4242Event{
					Event: federation.Event{
						Type:     spec.MRoomPowerLevels,
						Sender:   bob,
						StateKey: &empty,
						Content: map[string]interface{}{
							"users": map[string]any{
								charlie: 50,
							},
							"state_default": 50,
						},
					},
					PrevStateEvents: []string{
						charlieJoin.EventID(),
					},
				})
				room.AddEvent(charlieMod)
				charlieRoomName := mustCreateEvent(t, srv, room, MSC4242Event{
					Event: federation.Event{
						Type:     spec.MRoomName,
						Sender:   charlie,
						StateKey: &empty,
						Content: map[string]interface{}{
							"name": "I will be concurrently banned",
						},
						PrevEvents: []string{
							charlieMod.EventID(),
						},
					},
					PrevStateEvents: []string{
						charlieMod.EventID(),
					},
				})
				room.AddEvent(charlieRoomName)
				charlieBan := mustCreateEvent(t, srv, room, MSC4242Event{
					Event: federation.Event{
						Type:     spec.MRoomMember,
						Sender:   bob,
						StateKey: &charlie,
						Content: map[string]interface{}{
							"membership": "ban",
						},
						PrevEvents: []string{
							charlieMod.EventID(),
						},
					},
					PrevStateEvents: []string{
						charlieMod.EventID(),
					},
				})
				room.AddEvent(charlieBan)
				t.Logf(
					"bobJoin=%s charlieInvite=%s charlieJoin=%s charlieMod=%s charlieRoomName=%s charlieBan=%s",
					room.CurrentState(spec.MRoomMember, bob).EventID(),
					charlieInvite.EventID(),
					charlieJoin.EventID(),
					charlieMod.EventID(),
					charlieRoomName.EventID(),
					charlieBan.EventID(),
				)

				return []gomatrixserverlib.PDU{
					charlieInvite, charlieJoin, charlieMod, charlieRoomName, charlieBan,
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"room_version": roomVersion,
				"preset":       "trusted_private_chat", // Bob has the PL to send state events
				"invite":       []string{bob},
			})
			room, _ := MustJoinRoom(t, srv, deployment, spec.ServerName("hs1"), roomID, bob)
			// send the events.
			events := tc.sendEvents(t, room)
			eventJSONs := make([]json.RawMessage, len(events))
			eventIDs := make([]interface{}, len(events))
			for i := range events {
				eventJSONs[i] = events[i].JSON()
				eventIDs[i] = events[i].EventID()
			}
			srv.MustSendTransaction(t, deployment, "hs1", eventJSONs, nil)
			// we don't know when the server has processed these events sadly, as rejected events don't appear down /sync.
			// We must sleep prior to leaving as our leave event will reference events in the above transaction
			// potentially, and we don't want to cause /get_missing_events requests.
			time.Sleep(time.Second)
			// Leave so we can re-join and check the /send_join response
			srv.MustLeaveRoom(t, deployment, "hs1", roomID, bob)
			// now rejoin the room with /send_join => we should NOT see the two rejected events in the state DAG.
			alice.MustInviteRoom(t, roomID, bob)
			_, sendJoinResp := MustJoinRoom(t, srv, deployment, spec.ServerName("hs1"), roomID, bob)
			stateDAGEventIDs := make([]interface{}, len(sendJoinResp.StateDAG))
			for i, ev := range sendJoinResp.StateDAG.TrustedEvents(roomVersion, false) {
				stateDAGEventIDs[i] = ev.EventID()
			}
			stateDAGSet := set.NewSetFromSlice(stateDAGEventIDs)
			sendEventsSet := set.NewSetFromSlice(eventIDs)
			if tc.assertEventsAreInSendJoinResponse {
				must.Equal(
					t, sendEventsSet.IsSubset(stateDAGSet), true,
					fmt.Sprintf("Sent events were not all in the state dag.\nSent events: %v\nState DAG: %v", sendEventsSet.String(), stateDAGSet.String()),
				)
			} else {
				must.Equal(
					t, sendEventsSet.Intersect(stateDAGSet).Cardinality(), 0,
					fmt.Sprintf("Some sent events were in the state dag.\nSent events: %v\nState DAG: %v", sendEventsSet.String(), stateDAGSet.String()),
				)
			}
		})
	}
}

func sendTextMessages(t *testing.T, cli *client.CSAPI, roomID, prefix string, numTimes int) (eventIDs []string) {
	for i := 0; i < numTimes; i++ {
		// we need to send these synced to reduce the chance of self-forking as that would break /get_missing_events assertions.
		eventIDs = append(eventIDs, cli.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]any{
				"msgtype": "m.text",
				"body":    fmt.Sprintf("sendTextMessages %s %d", prefix, i),
			},
		}))
	}
	return
}
