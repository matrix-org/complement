package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/tidwall/gjson"
)

// STATE: Room state tests
//  STATE00: a valid message and state event are subsequently accepted if the prev_state_events cannot be found at first, but are found later.
//  STATE01: If a state dag outlier event is de-outliered, ensure room state at that event is correct (via /state_ids).
//  STATE02: prev_state_events changes to old, already seen state events, which does not update forward extremities. Current state does not change.
//  STATE03: XXX receiving a state event via /send which does NOT alter the current state (say it's a concurrent topic change which does not win state res)
//      should | should not go down /sync and be present in the timeline.
//  STATE04: mismatched prev_events and prev_state_events produces the correct state

func TestMSC4242STATE00TemporaryNetworkErrorIsOkay(t *testing.T) {
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
	srv.UnexpectedRequestsAreErrors = false
	cancel := srv.Listen()
	defer cancel()
	bob := srv.UserID("bob")
	room := srv.MustMakeRoom(t, roomVersion,
		federation.InitialRoomEvents(roomVersion, bob),
		federation.WithImpl(ServerRoomImplStateDAG(t, srv)),
	)
	alice.MustJoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})

	var getMissingEventsStateDAGHandler func(w http.ResponseWriter, req *fclient.MissingEvents)
	var getMissingEventsEventDAGHandler func(w http.ResponseWriter, req *fclient.MissingEvents)
	srv.Mux().HandleFunc("/_matrix/federation/v1/get_missing_events/{roomID}", func(w http.ResponseWriter, req *http.Request) {
		body, err := extractGetMissingEventsRequest(room.RoomID, req)
		if err != nil {
			ct.Errorf(t, "failed to read get_missing_events req body: %s", err)
			w.WriteHeader(500)
			return
		}
		if body.StateDAG {
			getMissingEventsStateDAGHandler(w, body)
		} else {
			getMissingEventsEventDAGHandler(w, body)
		}
	})

	// Create three events:
	//    A <-- missing, will be requested in /get_missing_events with state DAG
	//    |
	//    B <-- /get_missing_events (no state dag)
	//    |
	//    C <-- /send
	//    .
	//    D
	//
	// We will initially 502 the /get_missing_events (state DAG) request. This will cause
	// C to fail to be persisted. We'll then send D which we will then return C in /get_missing_events (no state DAG)
	// and then return [A,B] in /get_missing_events (state DAG). We should see A,B,C,D in /sync.

	// We want to test what happens if the events in /send are state events as well as if they are message events,
	// so we'll replay the following code twice
	testCases := []struct {
		name             string
		eventToStateKeys map[string]*string
	}{
		{
			name: "all events are state events",
			eventToStateKeys: map[string]*string{
				"A": &empty,
				"B": &empty,
				"C": &empty,
				"D": &empty,
			},
		},
		{
			name: "C & D are message events",
			eventToStateKeys: map[string]*string{
				"A": &empty,
				"B": &empty,
				"C": nil,
				"D": nil,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			events := make(map[string]gomatrixserverlib.PDU)
			for _, letter := range []string{"A", "B", "C", "D"} {
				stateKey := tc.eventToStateKeys[letter]
				ev := srv.MustCreateEvent(t, room, federation.Event{
					Type:     letter,
					StateKey: stateKey,
					Sender:   bob,
					Content:  map[string]interface{}{"event": letter},
				})
				room.AddEvent(ev)
				events[letter] = ev
				t.Logf("event %s => %s (prev_state_events=%v)", letter, ev.EventID(), ev.PrevStateEventIDs())
			}
			getMissingEventsEventDAGHandler = func(w http.ResponseWriter, req *fclient.MissingEvents) {
				if !slices.Equal(req.LatestEvents, []string{events["C"].EventID()}) {
					ct.Errorf(t, "unexpected /get_missing_events event graph request: latest=%v", req.LatestEvents)
					w.WriteHeader(400)
					return
				}
				w.WriteHeader(200)
				resp := fclient.RespMissingEvents{
					Events: gomatrixserverlib.NewEventJSONsFromEvents([]gomatrixserverlib.PDU{
						events["B"],
					}),
				}
				if err := json.NewEncoder(w).Encode(&resp); err != nil {
					must.NotError(t, "failed to encode response", err)
				}
			}
			waiter := helpers.NewWaiter()
			getMissingEventsStateDAGHandler = func(w http.ResponseWriter, _ *fclient.MissingEvents) {
				w.WriteHeader(502)
				waiter.Finish()
			}
			srv.MustSendTransaction(t, deployment, "hs1", AsEventJSONs([]gomatrixserverlib.PDU{events["C"]}), nil)

			// wait until we see the state DAG /gme request
			waiter.Waitf(t, 2*time.Second, "failed to see /get_missing_events state DAG request")

			// now send D and respond to everything correctly
			getMissingEventsEventDAGHandler = func(w http.ResponseWriter, req *fclient.MissingEvents) {
				if !slices.Equal(req.LatestEvents, []string{events["D"].EventID()}) {
					ct.Errorf(t, "unexpected /get_missing_events event graph request: latest=%v", req.LatestEvents)
					w.WriteHeader(400)
					return
				}
				w.WriteHeader(200)
				resp := fclient.RespMissingEvents{
					Events: gomatrixserverlib.NewEventJSONsFromEvents([]gomatrixserverlib.PDU{
						events["C"],
					}),
				}
				if err := json.NewEncoder(w).Encode(&resp); err != nil {
					must.NotError(t, "failed to encode response", err)
				}
			}
			getMissingEventsStateDAGHandler = func(w http.ResponseWriter, _ *fclient.MissingEvents) {
				w.WriteHeader(200)
				resp := fclient.RespMissingEvents{
					Events: gomatrixserverlib.NewEventJSONsFromEvents([]gomatrixserverlib.PDU{
						events["A"], events["B"], events["C"],
					}),
				}
				if err := json.NewEncoder(w).Encode(&resp); err != nil {
					must.NotError(t, "failed to encode response", err)
				}
			}
			srv.MustSendTransaction(t, deployment, "hs1", AsEventJSONs([]gomatrixserverlib.PDU{events["D"]}), nil)

			// we should see A,B,C,D
			seenEvents := make(map[string]bool)
			isLimited := false
			var prevBatch string
			alice.MustSyncUntil(t, client.SyncReq{
				Filter: `{"event_format":"federation","room":{"timeline":{"limit":50}}}`,
			},
				func(clientUserID string, topLevelSyncJSON gjson.Result) error {
					t.Log(topLevelSyncJSON.Raw)
					client.SyncStateHas(room.RoomID, func(r gjson.Result) bool {
						seenEvents[r.Get("event_id").Str] = true
						return false
					})(clientUserID, topLevelSyncJSON)
					client.SyncTimelineHas(room.RoomID, func(r gjson.Result) bool {
						seenEvents[r.Get("event_id").Str] = true
						return false
					})(clientUserID, topLevelSyncJSON)
					if seenEvents[events["D"].EventID()] {
						return nil
					}
					if topLevelSyncJSON.Get("rooms.join." + client.GjsonEscape(room.RoomID) + ".timeline.limited").Bool() {
						isLimited = true
						prevBatch = topLevelSyncJSON.Get(
							"rooms.join." + client.GjsonEscape(room.RoomID) + ".timeline.prev_batch",
						).Str
					}
					return fmt.Errorf("haven't seen event D yet")
				},
			)
			// Synapse for some reason returns a limited response which omits D despite setting a high limit,
			// so if we see that then request earlier messages. This only applies for the message events form
			if isLimited && !seenEvents[events["C"].EventID()] {
				for _, backfilledEvent := range backfill(t, alice, roomVersion, room.RoomID, prevBatch, 1) {
					t.Logf("/messages => %s", backfilledEvent.EventID())
					seenEvents[backfilledEvent.EventID()] = true
				}
			}
			must.Equal(t, seenEvents[events["A"].EventID()], true, "did not see event A")
			must.Equal(t, seenEvents[events["B"].EventID()], true, "did not see event B")
			must.Equal(t, seenEvents[events["C"].EventID()], true, "did not see event C")
			must.Equal(t, seenEvents[events["D"].EventID()], true, "did not see event D")
		})
	}
}

// The intention behind this test is to ensure that the outlier state => 'normal' state
// is transparently handled. This was more important back when we had auth DAGs, but is
// still a useful property to assert.
func TestMSC4242STATE01DeOutlieredStateIsCorrect(t *testing.T) {
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
	srv.UnexpectedRequestsAreErrors = false
	cancel := srv.Listen()
	defer cancel()
	bob := srv.UserID("bob")
	room := srv.MustMakeRoom(t, roomVersion,
		federation.InitialRoomEvents(roomVersion, bob),
		federation.WithImpl(ServerRoomImplStateDAG(t, srv)),
	)
	alice.MustJoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})
	t.Logf("Alice Join => %v", room.CurrentState(spec.MRoomMember, alice.UserID).EventID())

	var getMissingEventsStateDAGHandler func(w http.ResponseWriter, req *fclient.MissingEvents)
	var getMissingEventsEventDAGHandler func(w http.ResponseWriter, req *fclient.MissingEvents)
	srv.Mux().HandleFunc("/_matrix/federation/v1/get_missing_events/{roomID}", func(w http.ResponseWriter, req *http.Request) {
		body, err := extractGetMissingEventsRequest(room.RoomID, req)
		if err != nil {
			ct.Errorf(t, "failed to read get_missing_events req body: %s", err)
			w.WriteHeader(500)
			return
		}
		if body.StateDAG {
			getMissingEventsStateDAGHandler(w, body)
		} else {
			getMissingEventsEventDAGHandler(w, body)
		}
	})

	// Create a graph like this (type, state_key) $event_id
	//
	//      ALICE_JOIN
	//        |
	//       $MSG1
	//        |
	//      (A,"") $E1  <-.
	//        |            \
	//       $MSG2          \
	//        |              \
	//      (B,"") $E2  <-----\
	//        |                \
	//       $MSG3              /get_missing_events (state-dag)
	//        |                /
	//      (A,"") $E3  <-----/
	//        |              /
	//       $MSG4          /
	//        |            /
	//      (C,"") $E4  <-`
	//        |
	//       $MSG5
	//        |
	//       $MSG6      <--- /get_missing_events (non-state-dag)
	//        |
	//      (A,"")  $E5 <-- /send
	//
	// By doing this, $E1-4 are all outliers as they are not connected to the
	// messages sent. We ensure that when the HS fetches the state DAG that they cannot
	// correlate any happens-before relationship using prev_events (this is why we buffer
	// each state event with $MSGs). We then fill in the graph with /messages and ensure
	// that the state remains the same.
	events := make(map[string]gomatrixserverlib.PDU)
	var backfillEvents, eventAVersions, getMissingEventsStateDAG []gomatrixserverlib.PDU
	for _, letter := range []string{"MSG1", "A", "MSG2", "B", "MSG3", "A", "MSG4", "C", "MSG5", "MSG6", "A"} {
		var stateKey *string
		if !strings.HasPrefix(letter, "MSG") {
			stateKey = &empty
		}
		ev := srv.MustCreateEvent(t, room, federation.Event{
			Type:     letter,
			StateKey: stateKey,
			Sender:   bob,
			Content:  map[string]interface{}{"event": letter},
		})
		room.AddEvent(ev)
		events[letter] = ev // this means we clobber and only have the latest A value
		t.Logf("event %s => %s (prev_state_events=%v)", letter, ev.EventID(), ev.PrevStateEventIDs())
		// The first 4 state events should be returned in /get_missing_events (state-dag)
		if stateKey != nil && len(getMissingEventsStateDAG) < 4 {
			getMissingEventsStateDAG = append(getMissingEventsStateDAG, ev)
		}
		// Persist all A's so we can check state later
		if letter == "A" {
			eventAVersions = append(eventAVersions, ev)
		}
		backfillEvents = append(backfillEvents, ev)
	}

	srv.Mux().HandleFunc("/_matrix/federation/v1/backfill/{roomID}", func(w http.ResponseWriter, req *http.Request) {
		resp := struct {
			Events                gomatrixserverlib.EventJSONs `json:"pdus"`
			Origin                string                       `json:"origin"`
			OriginServerTimestamp int64                        `json:"origin_server_ts"`
		}{}
		resp.Origin = string(srv.ServerName())
		resp.OriginServerTimestamp = time.Now().UnixMilli()
		resp.Events = gomatrixserverlib.NewEventJSONsFromEvents(backfillEvents)
		w.WriteHeader(200)
		if err := json.NewEncoder(w).Encode(&resp); err != nil {
			ct.Errorf(t, "failed to encode /backfill response: %v", err)
		}
	})
	getMissingEventsEventDAGHandler = func(w http.ResponseWriter, req *fclient.MissingEvents) {
		if !slices.Equal(req.LatestEvents, []string{events["A"].EventID()}) {
			ct.Errorf(t, "unexpected /get_missing_events event graph request: latest=%v", req.LatestEvents)
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(200)
		resp := fclient.RespMissingEvents{
			Events: gomatrixserverlib.NewEventJSONsFromEvents([]gomatrixserverlib.PDU{
				events["MSG6"],
			}),
		}
		if err := json.NewEncoder(w).Encode(&resp); err != nil {
			must.NotError(t, "failed to encode response", err)
		}
	}
	getMissingEventsWaiter := helpers.NewWaiter()
	getMissingEventsStateDAGHandler = func(w http.ResponseWriter, _ *fclient.MissingEvents) {
		w.WriteHeader(200)
		resp := fclient.RespMissingEvents{
			Events: gomatrixserverlib.NewEventJSONsFromEvents(getMissingEventsStateDAG),
		}
		if err := json.NewEncoder(w).Encode(&resp); err != nil {
			must.NotError(t, "failed to encode response", err)
		}
		getMissingEventsWaiter.Finish()
	}

	// Send the LATEST A value and make sure we see it.
	srv.MustSendTransaction(t, deployment, "hs1", AsEventJSONs([]gomatrixserverlib.PDU{events["A"]}), nil)
	getMissingEventsWaiter.Waitf(t, 2*time.Second, "failed to see /get_missing_events (state dag) request")
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(room.RoomID, events["A"].EventID()))

	// Now backfill to de-outlier the state events
	for _, backfilledEvent := range backfill(t, alice, roomVersion, room.RoomID, "", 30) {
		t.Logf("/messages => %s", backfilledEvent.EventID())
	}

	// state should not have changed by de-outliering. We can't do this assertion prior to backfilling as
	// /state_ids cannot be used on outliers.
	initialStateEventIDs := []string{
		room.CurrentState(spec.MRoomCreate, "").EventID(),
		room.CurrentState(spec.MRoomPowerLevels, "").EventID(),
		room.CurrentState(spec.MRoomJoinRules, "").EventID(),
		room.CurrentState(spec.MRoomMember, alice.UserID).EventID(),
		room.CurrentState(spec.MRoomMember, bob).EventID(),
	}
	wantRoomStateBeforeEvent := map[string][]string{
		// This is the latest A event
		"A": append([]string{
			eventAVersions[len(eventAVersions)-2].EventID(), // previous A value
			events["B"].EventID(),
			events["C"].EventID(),
		}, initialStateEventIDs...),
		"MSG2": append([]string{
			eventAVersions[0].EventID(), // the first A value
		}, initialStateEventIDs...),
		"B": append([]string{
			eventAVersions[0].EventID(), // the first A value
		}, initialStateEventIDs...),
		"MSG3": append([]string{
			eventAVersions[0].EventID(), // the first A value
			events["B"].EventID(),
		}, initialStateEventIDs...),
		"MSG4": append([]string{
			eventAVersions[1].EventID(), // the second A value
			events["B"].EventID(),
		}, initialStateEventIDs...),
		"C": append([]string{
			eventAVersions[1].EventID(), // the 2nd A value
			events["B"].EventID(),
		}, initialStateEventIDs...),
		"MSG5": append([]string{
			eventAVersions[1].EventID(), // the 2nd A value
			events["B"].EventID(),
			events["C"].EventID(),
		}, initialStateEventIDs...),
		"MSG6": append([]string{
			eventAVersions[1].EventID(), // the 2nd A value
			events["B"].EventID(),
			events["C"].EventID(),
		}, initialStateEventIDs...),
	}

	for letter, wantStateEventIDs := range wantRoomStateBeforeEvent {
		t.Logf("the state before %v should be %v", letter, wantStateEventIDs)
		fedClient := srv.FederationClient(deployment)
		resp, err := fedClient.LookupStateIDs(
			context.Background(), spec.ServerName(srv.ServerName()), "hs1", room.RoomID, events[letter].EventID(),
		)
		must.NotError(t, "failed to /state_ids", err)
		slices.Sort(wantStateEventIDs)
		slices.Sort(resp.StateEventIDs)
		if !slices.Equal(resp.StateEventIDs, wantStateEventIDs) {
			ct.Errorf(t, "/state_ids response for event '%s' incorrect:\ngot  %v\nwant %v", letter, resp.StateEventIDs, wantStateEventIDs)
		}
	}
}

func TestMSC4242STATE02OldStateChangesBetweenEvents(t *testing.T) {
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
	srv.UnexpectedRequestsAreErrors = false
	cancel := srv.Listen()
	defer cancel()
	bob := srv.UserID("bob")
	room := srv.MustMakeRoom(t, roomVersion,
		federation.InitialRoomEvents(roomVersion, bob),
		federation.WithImpl(ServerRoomImplStateDAG(t, srv)),
	)
	alice.MustJoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})
	for _, ev := range room.Timeline {
		printInfo(t, ev.Type()+" "+*ev.StateKey(), ev)
	}

	// Create a prev_events graph like this:
	//            ALICE_JOIN
	//                 |
	//         BOB_SET_ROOM_NAME_A
	//                 |
	//         BOB_SET_ROOM_NAME_B
	//                 |
	//         BOB_SET_ROOM_TOPIC
	//                 |
	//               MSG (prev_state_events = BOB_SET_ROOM_NAME_A)
	//
	// Setting old prev_state_events should be a no-op. It should not rollback the current room state.
	// The state before the event will be the state given by prev_state_events and not the state at prev_events.
	//
	eventsToCreate := []federation.Event{
		{
			Type:     spec.MRoomName,
			StateKey: &empty,
			Sender:   bob,
			Content:  map[string]interface{}{"name": "A"},
		},
		{
			Type:     spec.MRoomName,
			StateKey: &empty,
			Sender:   bob,
			Content:  map[string]interface{}{"name": "B"},
		},
		{
			Type:     spec.MRoomTopic,
			StateKey: &empty,
			Sender:   bob,
			Content:  map[string]interface{}{"topic": "TOPIC"},
		},
	}
	var createdEvents []gomatrixserverlib.PDU
	for _, fev := range eventsToCreate {
		ev := srv.MustCreateEvent(t, room, fev)
		room.AddEvent(ev)
		createdEvents = append(createdEvents, ev)
		printInfo(t, fev.Type, ev)
	}
	// now make the bad event
	msg := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       "m.room.message",
			Sender:     bob,
			Content:    map[string]interface{}{"msgtype": "m.text", "body": "Test"},
			PrevEvents: []string{createdEvents[2].EventID()},
		},
		PrevStateEvents: []string{createdEvents[0].EventID()},
	})
	createdEvents = append(createdEvents, msg)
	printInfo(t, "msg", msg)

	// ..and send them all
	srv.MustSendTransaction(t, deployment, "hs1", AsEventJSONs(createdEvents), nil)

	// we should see all the created events as all of them are valid.
	wantEventIDs := AsEventIDs(t, createdEvents)
	var opts []client.SyncCheckOpt
	for _, eventID := range wantEventIDs {
		opts = append(opts, client.SyncTimelineHasEventID(room.RoomID, eventID))
	}
	alice.MustSyncUntil(t, client.SyncReq{}, opts...)

	// the current state of the room should be the state DAG's forward extremities.
	// To work out the current state, just do a single /sync with limit=1 which will return the message
	// event and put all the current state in 'state'

	/* Sync v2 TODO(kegan): this fails currently and DOES rollback.
	resp, _ := alice.MustSync(t, client.SyncReq{
		Filter: `{"event_format":"federation","room":{"timeline":{"limit":1}}}`,
	})
	events := resp.Get("rooms.join." + client.GjsonEscape(room.RoomID) + ".state.events").Array()
	gotStateIDs := make([]string, len(events))
	for i := range events {
		gotStateIDs[i] = events[i].Get("event_id").Str
	} */

	/* Sync v2 with MSC4222 state_after: MSC4222 is not enabled by default. Sad panda.
	httpResp := alice.MustDo(t, "GET", []string{
		"_matrix", "client", "v3", "sync",
	}, client.WithQueries(url.Values{
		"org.matrix.msc4222.use_state_after": []string{"true"},
	}))
	resp := gjson.ParseBytes(client.ParseJSON(t, httpResp))
	events := resp.Get("rooms.join." + client.GjsonEscape(room.RoomID) + "." + client.GjsonEscape("org.matrix.msc4222.state_after") + ".events").Array()
	gotStateIDs := make([]string, len(events))
	for i := range events {
		gotStateIDs[i] = events[i].Get("event_id").Str
	} */

	httpResp := alice.MustDo(t, "POST", []string{
		"_matrix", "client", "unstable", "org.matrix.simplified_msc3575", "sync",
	}, client.WithJSONBody(t, map[string]any{
		"lists": map[string]any{
			"a": map[string]any{
				"ranges":         [][2]int{{0, 100}},
				"timeline_limit": 1,
				"required_state": [][2]string{{"*", "*"}},
			},
		},
	}))
	resp := gjson.ParseBytes(client.ParseJSON(t, httpResp))
	events := resp.Get("rooms." + client.GjsonEscape(room.RoomID) + ".required_state").Array()
	gotStateIDs := make([]string, len(events))
	for i := range events {
		gotStateIDs[i] = events[i].Get("event_id").Str
	}

	t.Logf("gotStateIDs %v", gotStateIDs)

	wantStateIDs := AsEventIDs(t, room.AllCurrentState())
	slices.Sort(wantStateIDs)
	slices.Sort(gotStateIDs)
	if !reflect.DeepEqual(gotStateIDs, wantStateIDs) {
		ct.Errorf(t, "current state is incorrect.\ngot  %v\nwant %v", gotStateIDs, wantStateIDs)
	}
}

func TestMSC4242STATE04MismatchedPrevStateEventsVsPrevEvents(t *testing.T) {
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
	srv.UnexpectedRequestsAreErrors = false
	cancel := srv.Listen()
	defer cancel()
	bob := srv.UserID("bob")
	room := srv.MustMakeRoom(t, roomVersion,
		federation.InitialRoomEvents(roomVersion, bob),
		federation.WithImpl(ServerRoomImplStateDAG(t, srv)),
	)
	alice.MustJoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})

	aliceJoin := room.CurrentState(spec.MRoomMember, alice.UserID)
	printInfo(t, "Alice Join", aliceJoin)

	// Create a graph like this
	//   prev_events          prev_state_events
	//
	//           +------ALICE_JOIN
	//           |        |    |
	//           |    .---STATE0
	//           |   |         |
	//           +---|----STATE1
	//           |   |         |
	//           |    `---STATE2   <-- first batch
	//           |    `        |
	//           +---|--- STATE3
	//               |         |
	//                `---STATE4   <--- 2nd batch
	//
	// Servers should be ignoring prev_events when calculating state.
	// If they are, the state at STATE2 will include STATE1. If they are reading prev_events instead, then it will not include STATE1.
	// We do it in two batches to make sure deltas down /sync look correct.
	// There's no /get_missing_events stuff here, just manipulation of prev_events | prev_state_events.

	state0 := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       "state.0",
			StateKey:   &empty,
			Sender:     bob,
			Content:    map[string]interface{}{"state": "0"},
			PrevEvents: []string{aliceJoin.EventID()},
		},
		PrevStateEvents: []string{aliceJoin.EventID()},
	})
	state1 := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       "state.1",
			StateKey:   &empty,
			Sender:     bob,
			Content:    map[string]interface{}{"state": "1"},
			PrevEvents: []string{aliceJoin.EventID()},
		},
		PrevStateEvents: []string{state0.EventID()},
	})
	state2 := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       "state.2",
			StateKey:   &empty,
			Sender:     bob,
			Content:    map[string]interface{}{"state": "2"},
			PrevEvents: []string{state0.EventID()},
		},
		PrevStateEvents: []string{state1.EventID()},
	})
	state3 := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       "state.3",
			StateKey:   &empty,
			Sender:     bob,
			Content:    map[string]interface{}{"state": "3"},
			PrevEvents: []string{state1.EventID()},
		},
		PrevStateEvents: []string{state2.EventID()},
	})
	state4 := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       "state.4",
			StateKey:   &empty,
			Sender:     bob,
			Content:    map[string]interface{}{"state": "4"},
			PrevEvents: []string{state2.EventID()},
		},
		PrevStateEvents: []string{state3.EventID()},
	})

	infos := map[string]gomatrixserverlib.PDU{
		"state0": state0,
		"state1": state1,
		"state2": state2,
		"state3": state3,
		"state4": state4,
	}
	for k, v := range infos {
		printInfo(t, k, v)
	}

	// send the first batch
	srv.MustSendTransaction(t, deployment, "hs1", AsEventJSONs([]gomatrixserverlib.PDU{
		state0, state1, state2,
	}), nil)

	wantCurrentState := map[string]string{
		state0.Type(): state0.EventID(),
		state1.Type(): state1.EventID(),
		state2.Type(): state2.EventID(),
	}
	currentState := map[string]string{} // event type => event ID (ignoring state key as these state events are all "")
	since := alice.MustSyncUntil(t, client.SyncReq{}, func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		// State is before the timeline so apply it first.
		client.SyncStateHas(room.RoomID, func(r gjson.Result) bool {
			currentState[r.Get("type").Str] = r.Get("event_id").Str
			return false
		})(clientUserID, topLevelSyncJSON)
		client.SyncTimelineHas(room.RoomID, func(r gjson.Result) bool {
			currentState[r.Get("type").Str] = r.Get("event_id").Str
			return false
		})(clientUserID, topLevelSyncJSON)
		// we stop syncing when we see state2
		return client.SyncTimelineHasEventID(room.RoomID, state2.EventID())(clientUserID, topLevelSyncJSON)
	})
	for wantType, wantEventID := range wantCurrentState {
		if currentState[wantType] != wantEventID {
			ct.Errorf(t, "current state for %v is %v but wanted %v", wantType, currentState[wantType], wantEventID)
		}
	}

	// send the second batch
	srv.MustSendTransaction(t, deployment, "hs1", AsEventJSONs([]gomatrixserverlib.PDU{
		state3, state4,
	}), nil)
	wantCurrentState = map[string]string{
		state0.Type(): state0.EventID(),
		state1.Type(): state1.EventID(),
		state2.Type(): state2.EventID(),
		state3.Type(): state3.EventID(),
		state4.Type(): state4.EventID(),
	}
	alice.MustSyncUntil(t, client.SyncReq{Since: since}, func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		// State is before the timeline so apply it first.
		client.SyncStateHas(room.RoomID, func(r gjson.Result) bool {
			currentState[r.Get("type").Str] = r.Get("event_id").Str
			return false
		})(clientUserID, topLevelSyncJSON)
		client.SyncTimelineHas(room.RoomID, func(r gjson.Result) bool {
			currentState[r.Get("type").Str] = r.Get("event_id").Str
			return false
		})(clientUserID, topLevelSyncJSON)
		// we stop syncing when we see state4
		return client.SyncTimelineHasEventID(room.RoomID, state4.EventID())(clientUserID, topLevelSyncJSON)
	})
	for wantType, wantEventID := range wantCurrentState {
		if currentState[wantType] != wantEventID {
			ct.Errorf(t, "current state for %v is %v but wanted %v", wantType, currentState[wantType], wantEventID)
		}
	}

	// and now check /state_ids for completion
	initialStateEventIDs := []string{
		room.CurrentState(spec.MRoomCreate, "").EventID(),
		room.CurrentState(spec.MRoomPowerLevels, "").EventID(),
		room.CurrentState(spec.MRoomJoinRules, "").EventID(),
		room.CurrentState(spec.MRoomMember, alice.UserID).EventID(),
		room.CurrentState(spec.MRoomMember, bob).EventID(),
	}
	wantRoomStateBeforeEvent := map[string][]string{
		state0.EventID(): initialStateEventIDs,
		state1.EventID(): append([]string{state0.EventID()}, initialStateEventIDs...),
		state2.EventID(): append([]string{state0.EventID(), state1.EventID()}, initialStateEventIDs...),
		state3.EventID(): append([]string{state0.EventID(), state1.EventID(), state2.EventID()}, initialStateEventIDs...),
		state4.EventID(): append([]string{state0.EventID(), state1.EventID(), state2.EventID(), state3.EventID()}, initialStateEventIDs...),
	}
	for beforeEventID, wantStateEventIDs := range wantRoomStateBeforeEvent {
		t.Logf("the state before %v should be %v", beforeEventID, wantStateEventIDs)
		fedClient := srv.FederationClient(deployment)
		resp, err := fedClient.LookupStateIDs(
			context.Background(), spec.ServerName(srv.ServerName()), "hs1", room.RoomID, beforeEventID,
		)
		must.NotError(t, "failed to /state_ids", err)
		slices.Sort(wantStateEventIDs)
		slices.Sort(resp.StateEventIDs)
		if !slices.Equal(resp.StateEventIDs, wantStateEventIDs) {
			ct.Errorf(t, "/state_ids response for event '%s' incorrect:\ngot  %v\nwant %v", beforeEventID, resp.StateEventIDs, wantStateEventIDs)
		}
	}
}

func printInfo(t ct.TestLike, prefix string, p gomatrixserverlib.PDU) {
	t.Logf("%s => %v (prev_state_events=%v     prev_events=%v)", prefix, p.EventID(), p.PrevStateEventIDs(), p.PrevEventIDs())
}
