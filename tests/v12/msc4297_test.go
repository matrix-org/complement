package tests

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/matrix-org/util"
	"github.com/tidwall/gjson"
)

// Test that v2.1 has implemented starting from the empty set not the unconflicted set
// This test assumes a few things about the underlying server implementation:
//   - It eventually gives up calling /get_missing_events for some n < 250 and hits /state or /state_ids for the historical state.
//   - It does not call /event_auth but may call /event/{eventID}
//   - On encountering DAG gaps, the current state is the resolution of all the forwards extremities for each section.
//     In other words, the server calculates the current state as the merger of (what_we_knew_before, what_we_know_now),
//     despite there being no events with >1 prev_events.
//
// The scenario in this test is similar to the one in the MSC but different in two key ways:
//   - To force incorrect state, "Charlie changes display name" happens 250 times to force a /state{_ids} request.
//     In the MSC this only happened once.
//   - "Bob changes display name" does not exist. We rely on the server calculating the current state as the
//     merging of the forwards extremitiy before the gap and the forwards extremity after the gap, so
//     in other words we apply state resolution to (Alice leave, 250th Charlie display name change).
func TestMSC4297StateResolutionV2_1_starts_from_empty_set(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // needs additional fixes
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(nil, nil),
		federation.HandleEventRequests(),
	)
	srv.UnexpectedRequestsAreErrors = false
	cancel := srv.Listen()
	defer cancel()
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "alice"})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "bob"})
	charlie := srv.UserID("charlie")

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"room_version": roomVersion12,
		"preset":       "public_chat",
	})
	bob.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})
	room := srv.MustJoinRoom(t, deployment, "hs1", roomID, charlie, federation.WithRoomOpts(federation.WithImpl(&V12ServerRoom)))
	joinRulePublic := room.CurrentState(spec.MRoomJoinRules, "")
	aliceJoin := room.CurrentState(spec.MRoomMember, alice.UserID)
	synchronisationEventID := bob.SendEventSynced(t, room.RoomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "can you hear me charlie?",
		},
	})
	room.WaiterForEvent(synchronisationEventID).Waitf(t, 5*time.Second, "failed to see synchronisation event, is federation working?")

	// Alice makes the room invite-only then leaves
	joinRuleInviteOnlyEventID := alice.SendEventSynced(t, roomID, b.Event{
		Type:     spec.MRoomJoinRules,
		StateKey: b.Ptr(""),
		Sender:   alice.UserID,
		Content: map[string]interface{}{
			"join_rule": "invite",
		},
	})
	room.WaiterForEvent(joinRuleInviteOnlyEventID).Waitf(t, 5*time.Second, "failed to see invite join rule event")

	alice.MustLeaveRoom(t, roomID)
	// Wait for Charlie to see it
	time.Sleep(time.Second)
	aliceLeaveEvent := room.CurrentState(spec.MRoomMember, alice.UserID)
	if membership, err := aliceLeaveEvent.Membership(); err != nil || membership != spec.Leave {
		ct.Fatalf(t, "failed to see Alice leave the room, alice event is %s", string(aliceLeaveEvent.JSON()))
	}

	// Now only Bob (server under test) and Charlie (Complement server) are left in the room.
	// Charlie is going to send an event with unknown prev_event, causing /get_missing_events
	// until eventually /state_ids is hit. When it is, we'll return incorrect room state, claiming
	// that the current join rule is public, not invite. This will cause the join rules to get conflicted
	// and replayed. V2 would base the checks off the unconflicted state, and since all servers agree
	// that Alice=leave it would start like that, making the join rule transitions invalid and causing the
	// room to have no join rule at all. V2.1 fixes this by loading the auth_events of the event being replayed
	// which correctly has Alice joined. Alice isn't automatically re-joined to the room though because the
	// last step of the algorithm is to apply the unconflicted state on top of the resolved conflicts, without
	// any extra checks.

	// We don't know how far back server impls will go, so let's use 250 as a large enough value.
	charlieEvents := make([]gomatrixserverlib.PDU, 250)
	eventIDToIndex := make(map[string]int)
	for i := range charlieEvents {
		ev := srv.MustCreateEvent(t, room, federation.Event{
			Type:     spec.MRoomMember,
			StateKey: &charlie,
			Sender:   charlie,
			Content: map[string]interface{}{
				"membership":  spec.Join,
				"displayname": fmt.Sprintf("Charlie %d", i),
			},
		})
		room.AddEvent(ev)
		charlieEvents[i] = ev
		eventIDToIndex[ev.EventID()] = i
	}

	seenStateIDs := helpers.NewWaiter()
	// process requests to walk back through charlie's display name changes
	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/get_missing_events/{roomID}",
		srv.ValidFederationRequest(t, getMissingEventsHandler(t, room, eventIDToIndex, charlieEvents)),
	)

	getIncorrectState := func(atEventID string) (authChain, stateEvents []gomatrixserverlib.PDU) {
		t.Logf("calculating state before event %v (i=%v)", atEventID, eventIDToIndex[atEventID])
		// Find the correct state for charlie
		// we want to return the state before the requested event hence -1
		stateEvents = append(stateEvents, charlieEvents[eventIDToIndex[atEventID]-1])
		// charlie's auth events are everything prior to this
		authChain = append(authChain, charlieEvents[:eventIDToIndex[atEventID]-1]...)

		stateEvents = append(stateEvents,
			room.CurrentState(spec.MRoomCreate, ""),
			room.CurrentState(spec.MRoomPowerLevels, ""),
			room.CurrentState(spec.MRoomMember, bob.UserID),
			joinRulePublic, // This is wrong, and should be invite.
			aliceLeaveEvent,
		)
		authChain = append(authChain, aliceJoin)
		return
	}
	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/state_ids/{roomID}",
		srv.ValidFederationRequest(t, func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
			if pathParams["roomID"] != room.RoomID {
				t.Errorf("Received /state_ids for the wrong room: %s", room.RoomID)
				return util.JSONResponse{
					Code: 400,
					JSON: "wrong room",
				}
			}
			defer seenStateIDs.Finish()
			req, err := fr.HTTPRequest()
			must.NotError(t, "failed to get fed http reqest", err)
			atEventID := req.URL.Query().Get("event_id")
			t.Logf("received /state_ids at event %v (i=%v)", atEventID, eventIDToIndex[atEventID])
			authChain, stateEvents := getIncorrectState(atEventID)
			return util.JSONResponse{
				Code: 200,
				JSON: struct {
					AuthChainIDs []string `json:"auth_chain_ids"`
					PDUIDs       []string `json:"pdu_ids"`
				}{
					AuthChainIDs: asEventIDs(authChain),
					PDUIDs:       asEventIDs(stateEvents),
				},
			}
		}),
	)
	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/state/{roomID}",
		srv.ValidFederationRequest(t, func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
			if pathParams["roomID"] != room.RoomID {
				t.Errorf("Received /state for the wrong room: %s", room.RoomID)
				return util.JSONResponse{
					Code: 400,
					JSON: "wrong room",
				}
			}
			defer seenStateIDs.Finish()
			req, err := fr.HTTPRequest()
			must.NotError(t, "failed to get fed http reqest", err)
			atEventID := req.URL.Query().Get("event_id")
			t.Logf("received /state at event %v (i=%v)", atEventID, eventIDToIndex[atEventID])
			authChain, stateEvents := getIncorrectState(atEventID)
			return util.JSONResponse{
				Code: 200,
				JSON: struct {
					AuthChain gomatrixserverlib.EventJSONs `json:"auth_chain"`
					PDUs      gomatrixserverlib.EventJSONs `json:"pdus"`
				}{
					AuthChain: gomatrixserverlib.NewEventJSONsFromEvents(authChain),
					PDUs:      gomatrixserverlib.NewEventJSONsFromEvents(stateEvents),
				},
			}
		}),
	)

	// Send the last event to trigger /get_missing_events and /state_ids
	finalEvent := charlieEvents[len(charlieEvents)-1]
	srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{
		finalEvent.JSON(),
	}, nil)

	seenStateIDs.Waitf(t, 5*time.Second, "failed to see a /state{_ids} request")

	// wait until bob sees the final event
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(room.RoomID, finalEvent.EventID()))

	// the join rules should be `invite`.
	// Servers which do not implement v2.1 will get a HTTP 404 here.
	content := bob.MustGetStateEventContent(t, room.RoomID, spec.MRoomJoinRules, "")
	must.MatchGJSON(t, content, match.JSONKeyEqual("join_rule", "invite"))
}

func TestMSC4297StateResolutionV2_1_includes_conflicted_subgraph(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // needs additional fixes
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(nil, nil),
		federation.HandleEventRequests(),
	)
	srv.UnexpectedRequestsAreErrors = false
	cancel := srv.Listen()
	defer cancel()
	creator := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "creator"})
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "alice"})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "bob"})
	charlie := srv.UserID("charlie")
	eve := srv.UserID("eve")
	zara := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "zara"})

	// We play every event from Problem B from the MSC except for Eve's events. We separate out
	// Alice and Creator roles for combined MSC compatibility.
	roomID := creator.MustCreateRoom(t, map[string]interface{}{
		"room_version": roomVersion12,
		"preset":       "public_chat",
	})
	creator.SendEventSynced(t, roomID, b.Event{
		Type:     spec.MRoomPowerLevels,
		StateKey: b.Ptr(""),
		Content: map[string]any{
			"users": map[string]any{
				alice.UserID: 100,
			},
		},
	})
	alice.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})
	bob.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})
	room := srv.MustJoinRoom(t, deployment, "hs1", roomID, charlie, federation.WithRoomOpts(federation.WithImpl(&V12ServerRoom)))
	firstPowerLevelEvent := room.CurrentState(spec.MRoomPowerLevels, "")
	alice.SendEventSynced(t, roomID, b.Event{
		Type:     spec.MRoomPowerLevels,
		StateKey: b.Ptr(""),
		Content: map[string]any{
			"users": map[string]any{
				alice.UserID: 100,
				bob.UserID:   50,
			},
		},
	})
	pl3EventID := bob.SendEventSynced(t, roomID, b.Event{
		Type:     spec.MRoomPowerLevels,
		StateKey: b.Ptr(""),
		Content: map[string]any{
			"users": map[string]any{
				alice.UserID: 100,
				bob.UserID:   50,
				charlie:      50,
			},
		},
	})
	zara.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})

	// Now Eve will join from the third PL event
	eveJoinEvent := srv.MustCreateEvent(t, room, federation.Event{
		Type:     spec.MRoomMember,
		StateKey: &eve,
		Sender:   eve,
		Content: map[string]interface{}{
			"membership": spec.Join,
		},
		PrevEvents: []string{
			pl3EventID,
		},
	})
	room.AddEvent(eveJoinEvent)
	srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{eveJoinEvent.JSON()}, nil)
	aliceSince := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, eveJoinEvent.EventID()))

	// Now change Eve's display name many times to force partial synchronisation

	// We don't know how far back server impls will go, so let's use 250 as a large enough value.
	eveEvents := make([]gomatrixserverlib.PDU, 250)
	eventIDToIndex := make(map[string]int)
	prevEvents := eveJoinEvent.EventID()
	for i := range eveEvents {
		ev := srv.MustCreateEvent(t, room, federation.Event{
			Type:     spec.MRoomMember,
			StateKey: &eve,
			Sender:   eve,
			Content: map[string]interface{}{
				"membership":  spec.Join,
				"displayname": fmt.Sprintf("Eve %d", i),
			},
			PrevEvents: []string{prevEvents},
		})
		room.AddEvent(ev)
		eveEvents[i] = ev
		eventIDToIndex[ev.EventID()] = i
		prevEvents = ev.EventID()
	}

	seenStateIDs := helpers.NewWaiter()

	// process requests to walk back through eve's display name changes
	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/get_missing_events/{roomID}",
		srv.ValidFederationRequest(t, getMissingEventsHandler(t, room, eventIDToIndex, eveEvents)),
	)

	getIncorrectState := func(atEventID string) (authChain, stateEvents []gomatrixserverlib.PDU) {
		t.Logf("calculating state before event %v (i=%v)", atEventID, eventIDToIndex[atEventID])
		// Find the correct state for eve
		// we want to return the state before the requested event hence -1
		stateEvents = append(stateEvents, eveEvents[eventIDToIndex[atEventID]-1])
		// charlie's auth events are everything prior to this
		authChain = append(authChain, eveEvents[:eventIDToIndex[atEventID]-1]...)

		stateEvents = append(stateEvents,
			room.CurrentState(spec.MRoomCreate, ""),
			room.CurrentState(spec.MRoomMember, alice.UserID),
			firstPowerLevelEvent, // This is wrong, and should be the 3rd PL event.
			room.CurrentState(spec.MRoomJoinRules, ""),
			room.CurrentState(spec.MRoomMember, bob.UserID),
			room.CurrentState(spec.MRoomMember, charlie),
		)
		authChain = append(authChain, eveJoinEvent)
		return
	}
	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/state_ids/{roomID}",
		srv.ValidFederationRequest(t, func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
			if pathParams["roomID"] != room.RoomID {
				t.Errorf("Received /state_ids for the wrong room: %s", room.RoomID)
				return util.JSONResponse{
					Code: 400,
					JSON: "wrong room",
				}
			}
			defer seenStateIDs.Finish()
			req, err := fr.HTTPRequest()
			must.NotError(t, "failed to get fed http reqest", err)
			atEventID := req.URL.Query().Get("event_id")
			t.Logf("received /state_ids at event %v (i=%v)", atEventID, eventIDToIndex[atEventID])
			authChain, stateEvents := getIncorrectState(atEventID)
			return util.JSONResponse{
				Code: 200,
				JSON: struct {
					AuthChainIDs []string `json:"auth_chain_ids"`
					PDUIDs       []string `json:"pdu_ids"`
				}{
					AuthChainIDs: asEventIDs(authChain),
					PDUIDs:       asEventIDs(stateEvents),
				},
			}
		}),
	)
	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/state/{roomID}",
		srv.ValidFederationRequest(t, func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
			if pathParams["roomID"] != room.RoomID {
				t.Errorf("Received /state for the wrong room: %s", room.RoomID)
				return util.JSONResponse{
					Code: 400,
					JSON: "wrong room",
				}
			}
			defer seenStateIDs.Finish()
			req, err := fr.HTTPRequest()
			must.NotError(t, "failed to get fed http reqest", err)
			atEventID := req.URL.Query().Get("event_id")
			t.Logf("received /state at event %v (i=%v)", atEventID, eventIDToIndex[atEventID])
			authChain, stateEvents := getIncorrectState(atEventID)
			return util.JSONResponse{
				Code: 200,
				JSON: struct {
					AuthChain gomatrixserverlib.EventJSONs `json:"auth_chain"`
					PDUs      gomatrixserverlib.EventJSONs `json:"pdus"`
				}{
					AuthChain: gomatrixserverlib.NewEventJSONsFromEvents(authChain),
					PDUs:      gomatrixserverlib.NewEventJSONsFromEvents(stateEvents),
				},
			}
		}),
	)

	// Send the last event to trigger /get_missing_events and /state_ids
	finalEvent := eveEvents[len(eveEvents)-1]
	srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{
		finalEvent.JSON(),
	}, nil)

	seenStateIDs.Waitf(t, 5*time.Second, "failed to see a /state{_ids} request")

	// wait until bob sees the final event
	alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncTimelineHasEventID(room.RoomID, finalEvent.EventID()))

	// the power levels should be the 3rd one (Alice:PL100, Bob:PL50, Charlie:PL59), not the 1st (Alice: PL100).
	// Servers which do not implement v2.1 will see the 1st one.
	content := alice.MustGetStateEventContent(t, room.RoomID, spec.MRoomPowerLevels, "")
	must.MatchGJSON(t, content, match.JSONKeyEqual("users."+client.GjsonEscape(alice.UserID), 100))
	// v2 Servers will fail here as bob does not exist in this map because it reset to an earlier event.
	must.MatchGJSON(t, content, match.JSONKeyEqual("users."+client.GjsonEscape(bob.UserID), 50))
	must.MatchGJSON(t, content, match.JSONKeyEqual("users."+client.GjsonEscape(charlie), 50))
}

func getMissingEventsHandler(t ct.TestLike, room *federation.ServerRoom, eventIDToIndex map[string]int, events []gomatrixserverlib.PDU) func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
	return func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
		if pathParams["roomID"] != room.RoomID {
			t.Errorf("Received /get_missing_events for the wrong room: %s", room.RoomID)
			return util.JSONResponse{
				Code: 400,
				JSON: "wrong room",
			}
		}
		latestEventID := gjson.ParseBytes(fr.Content()).Get("latest_events").Array()[0].Str
		j, ok := eventIDToIndex[latestEventID]
		if !ok {
			ct.Fatalf(t, "received /get_missing_events with latest=%s which is unknown", latestEventID)
		}
		t.Logf("received /get_missing_events with latest=%s (i=%d)", latestEventID, j)
		// return 10 earlier events.
		slice := events[j-10 : j]
		for _, ev := range slice {
			t.Logf("/get_missing_events returning %v (i=%v)", ev.EventID(), eventIDToIndex[ev.EventID()])
		}
		return util.JSONResponse{
			Code: 200,
			JSON: struct {
				Events gomatrixserverlib.EventJSONs `json:"events"`
			}{
				Events: gomatrixserverlib.NewEventJSONsFromEvents(slice),
			},
		}
	}
}

func asEventIDs(pdus []gomatrixserverlib.PDU) []string {
	eventIDs := make([]string, len(pdus))
	for i := range pdus {
		eventIDs[i] = pdus[i].EventID()
	}
	return eventIDs
}
