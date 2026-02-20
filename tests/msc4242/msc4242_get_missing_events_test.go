package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync/atomic"
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

// GME: Get Missing Events tests (/get_missing_events)
//  Note: Outbound tests are tested by hitting /send with an event with unknown prev_state_events to
//        force the HS under test to do /get_missing_events.
//  GME00: if state_dag is not set, walks the room as usual via prev_events.
//  GME01(IO): if state_dag is set, walks the state dag breadth first
//    A: Linearly (1 prev_state_event)
//    B: With multiple parents (>1 prev_state_events)
//  GME02: Bad inputs
//    A: Returns nothing if you provide a message event ID.
//    B: Returns nothing if you provide a bogus event ID.
//  GME03: Faulty events:
//    A: it includes soft-failed events when walking.
//    B: it returns nothing if you provide a rejected event ID
//    C: it returns nothing if you provide a valid event which has all rejected event IDs as a predecessor
//    D: it returns nothing if you provide a valid event which has a rejected event ID as a predecessor
//  GME04: it terminates when the create event is reached.
//  GME05: when filling in the state DAG, it fails the incoming /send event if /get_missing_events returns:
//   A: no events
//   B: bogus events (e.g not events referenced in prev_state_events)
//   C: partial events (e.g 3 prev state events and only send the same 2)
//   D: an HTTP error

func TestMSC4242GetMissingEventsInbound(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

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

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := srv.UserID("bob")

	generateMessages := func(room *federation.ServerRoom, numMsgs int, addEventsToRoom bool) (eventsToSend []gomatrixserverlib.PDU) {
		for i := 0; i < numMsgs; i++ {
			pdu := srv.MustCreateEvent(t, room, federation.Event{
				Type:   "m.room.message",
				Sender: bob,
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    fmt.Sprintf("I am message %d", i),
				},
			})
			if addEventsToRoom {
				room.AddEvent(pdu)
			}
			eventsToSend = append(eventsToSend, pdu)
		}
		return eventsToSend
	}
	// Repeatedly calls /get_missing_events on hs1 starting from fromEvent, in batches of batchSize.
	// If stateDAG is set, asks the server to walk the state DAG.
	// Asserts that events are returned (over multiple requests) in the order provided by eventIDsPerRequest.
	assertGetMissingEventsRecursively := func(
		name string, roomID string, fromEvent gomatrixserverlib.PDU, getMissingEventsLimit int, stateDAG bool, eventIDsPerRequest []string,
	) {
		t.Helper()
		sg := NewStateGraph()
		if !stateDAG {
			sg.WalkPrevEvents = true
		}
		sg.Update([]gomatrixserverlib.PDU{fromEvent})
		startIndex := 0
		backwardsExtremities := []gomatrixserverlib.PDU{fromEvent}
		for {
			endIndex := startIndex + getMissingEventsLimit
			if endIndex > len(eventIDsPerRequest) {
				endIndex = len(eventIDsPerRequest)
			}
			wantEventIDs := eventIDsPerRequest[startIndex:endIndex]
			resp, err := srv.FederationClient(deployment).LookupMissingEvents(
				context.Background(), spec.ServerName(srv.ServerName()),
				deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
				roomID, fclient.MissingEvents{
					Limit:          getMissingEventsLimit,
					EarliestEvents: []string{},
					LatestEvents:   AsEventIDs(t, backwardsExtremities),
					StateDAG:       stateDAG,
				}, roomVersion,
			)
			must.NotError(t, "failed to send /get_missing_events request", err)
			pdus := resp.Events.TrustedEvents(roomVersion, false)
			got := AsEventIDs(t, pdus)
			if !stateDAG {
				// /gme returns in 'chronological' order  for non-state DAG requests, so flip it to get the same ordering
				// as state dag requests.
				slices.Reverse(got)
			}
			t.Logf("assertGetMissingEvents startIndex=%d endIndex=%d gotEventIDs=%v", startIndex, endIndex, got)
			if !slices.Equal(got, wantEventIDs) {
				ct.Errorf(t, "failed to see correct event IDs in test '%s'. \nGot  %v \nWant %v", name, got, wantEventIDs)
				return
			}
			if endIndex == len(eventIDsPerRequest) {
				break // we got to the end of the events
			}
			startIndex += getMissingEventsLimit // next batch plz

			// figure out the new backwards extremities
			sg.Update(pdus)
			backwardsExtremities = sg.BackwardsExtremities(backwardsExtremities)
		}
	}

	testCases := []struct {
		testCode             string
		name                 string
		generateEventsToWalk func(room *federation.ServerRoom) (events []gomatrixserverlib.PDU)
		// Expected order of events we expect to see as from /get_missing_events as we paginate
		wantWalkOrder         func(room *federation.ServerRoom, initialEvents, generatedEvents []gomatrixserverlib.PDU) (eventIDs []string)
		getMissingEventsLimit int
		walkStateDAG          bool
	}{
		{
			testCode:              "GME00",
			name:                  "state_dag=false walks prev_events",
			getMissingEventsLimit: 2,
			walkStateDAG:          false,
			generateEventsToWalk: func(room *federation.ServerRoom) (events []gomatrixserverlib.PDU) {
				events = generateDisplayNameChanges(t, srv, room, bob, 5, true)
				events = append(events, generateMessages(room, 5, true)...)
				return
			},
			wantWalkOrder: func(room *federation.ServerRoom, initialEvents, generatedEvents []gomatrixserverlib.PDU) (eventIDs []string) {
				// all events
				eventIDs = AsEventIDs(t, append(
					slices.Clone(initialEvents), generatedEvents...,
				))
				slices.Reverse(eventIDs)
				return eventIDs
			},
		},
		{
			testCode:              "GME01A-Inbound",
			name:                  "linear walking of the state dag",
			getMissingEventsLimit: 2,
			walkStateDAG:          true,
			generateEventsToWalk: func(room *federation.ServerRoom) (events []gomatrixserverlib.PDU) {
				events = generateDisplayNameChanges(t, srv, room, bob, 5, true)
				events = append(events, generateMessages(room, 5, true)...)
				return
			},
			wantWalkOrder: func(room *federation.ServerRoom, initialEvents, generatedEvents []gomatrixserverlib.PDU) (eventIDs []string) {
				// only state events
				var state []gomatrixserverlib.PDU
				for _, ev := range generatedEvents {
					if ev.StateKey() == nil {
						continue
					}
					state = append(state, ev)
				}
				eventIDs = AsEventIDs(t, append(
					slices.Clone(initialEvents), state...,
				))
				slices.Reverse(eventIDs)
				return eventIDs
			},
		},
		{
			testCode:              "GME01B-Inbound",
			name:                  "branch walking of the state dag",
			getMissingEventsLimit: 2,
			walkStateDAG:          true,
			generateEventsToWalk: func(room *federation.ServerRoom) (events []gomatrixserverlib.PDU) {
				stateFwdExtrem := room.Timeline[len(room.Timeline)-1].EventID()
				// Generate the following graph:
				//    A
				//   / \
				//  B   C
				//  |   |
				//  D   E
				eventA := mustCreateEvent(t, srv, room, MSC4242Event{
					Event: federation.Event{
						Type:     spec.MRoomMember,
						Sender:   bob,
						StateKey: &bob,
						Content: map[string]interface{}{
							"membership":  spec.Join,
							"displayname": "A",
						},
						PrevEvents: []string{stateFwdExtrem},
					},
					PrevStateEvents: []string{stateFwdExtrem},
				})
				eventB := mustCreateEvent(t, srv, room, MSC4242Event{
					Event: federation.Event{
						Type:     spec.MRoomMember,
						Sender:   bob,
						StateKey: &bob,
						Content: map[string]interface{}{
							"membership":  spec.Join,
							"displayname": "B",
						},
						PrevEvents: []string{eventA.EventID()},
					},
					PrevStateEvents: []string{eventA.EventID()},
				})
				eventC := mustCreateEvent(t, srv, room, MSC4242Event{
					Event: federation.Event{
						Type:     spec.MRoomMember,
						Sender:   bob,
						StateKey: &bob,
						Content: map[string]interface{}{
							"membership":  spec.Join,
							"displayname": "C",
						},
						PrevEvents: []string{eventA.EventID()},
					},
					PrevStateEvents: []string{eventA.EventID()},
				})
				eventD := mustCreateEvent(t, srv, room, MSC4242Event{
					Event: federation.Event{
						Type:     spec.MRoomMember,
						Sender:   bob,
						StateKey: &bob,
						Content: map[string]interface{}{
							"membership":  spec.Join,
							"displayname": "D",
						},
						PrevEvents: []string{eventB.EventID()},
					},
					PrevStateEvents: []string{eventB.EventID()},
				})
				eventE := mustCreateEvent(t, srv, room, MSC4242Event{
					Event: federation.Event{
						Type:     spec.MRoomMember,
						Sender:   bob,
						StateKey: &bob,
						Content: map[string]interface{}{
							"membership":  spec.Join,
							"displayname": "E",
						},
						PrevEvents: []string{eventC.EventID()},
					},
					PrevStateEvents: []string{eventC.EventID()},
				})
				events = append(events, eventA, eventB, eventC, eventD, eventE)
				for _, ev := range events {
					room.AddEvent(ev)
				}
				// set these so the sentinel points to both forks
				room.ForwardExtremities = []string{
					eventD.EventID(), eventE.EventID(),
				}
				return
			},
			wantWalkOrder: func(room *federation.ServerRoom, initialEvents, generatedEvents []gomatrixserverlib.PDU) (eventIDs []string) {
				lookup := map[string]int{"A": 0, "B": 1, "C": 2, "D": 3, "E": 4} // indexes map to generatedEvents
				// We will first return D,E, the ascii lowest one first
				if generatedEvents[lookup["D"]].EventID() < generatedEvents[lookup["E"]].EventID() {
					eventIDs = append(eventIDs, generatedEvents[lookup["D"]].EventID(), generatedEvents[lookup["E"]].EventID())
					// now we will walk back D first, then E (because the HS sorts latest_events then walks each in turn)
					eventIDs = append(eventIDs, generatedEvents[lookup["D"]].PrevStateEventIDs()...)
					eventIDs = append(eventIDs, generatedEvents[lookup["E"]].PrevStateEventIDs()...)
				} else {
					eventIDs = append(eventIDs, generatedEvents[lookup["E"]].EventID(), generatedEvents[lookup["D"]].EventID())
					// now we will walk back E first, then D (because the HS sorts latest_events then walks each in turn)
					eventIDs = append(eventIDs, generatedEvents[lookup["E"]].PrevStateEventIDs()...)
					eventIDs = append(eventIDs, generatedEvents[lookup["D"]].PrevStateEventIDs()...)
				}

				// finally A
				eventIDs = append(eventIDs, generatedEvents[lookup["A"]].EventID())

				// then the initial events reversed
				slices.Reverse(initialEvents)
				eventIDs = append(eventIDs, AsEventIDs(t, initialEvents)...)
				return eventIDs
			},
		},
		{
			testCode:              "GME04",
			name:                  "terminates when it reaches the create event",
			getMissingEventsLimit: 100, // larger limit than # events
			walkStateDAG:          true,
			generateEventsToWalk: func(room *federation.ServerRoom) (events []gomatrixserverlib.PDU) {
				return nil
			},
			wantWalkOrder: func(room *federation.ServerRoom, initialEvents, generatedEvents []gomatrixserverlib.PDU) (eventIDs []string) {
				if initialEvents[0].Type() != spec.MRoomCreate {
					t.Fatalf("initialEvents[0] must be the create event")
				}
				eventIDs = AsEventIDs(t, initialEvents)
				slices.Reverse(eventIDs)
				return eventIDs
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testCode, func(t *testing.T) {
			room := srv.MustMakeRoom(t, roomVersion,
				federation.InitialRoomEvents(roomVersion, bob),
				federation.WithImpl(ServerRoomImplStateDAG(t, srv)),
			)
			roomID := room.RoomID
			alice.MustJoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})
			initialEvents := make([]gomatrixserverlib.PDU, len(room.Timeline))
			for i := range initialEvents {
				initialEvents[i] = room.Timeline[i]
			}

			eventsToSend := tc.generateEventsToWalk(room)
			for _, ev := range room.Timeline {
				t.Logf("%s %s (%s) => prev_state_events = %v", tc.testCode, ev.EventID(), ev.Type(), gjson.GetBytes(ev.JSON(), "prev_state_events").Raw)
			}
			wantEventIDs := tc.wantWalkOrder(room, initialEvents, eventsToSend)

			// send sentinel which, when we see it, we know the homeserver has all earlier events
			// This has to be a state event for cases where we walk the state DAG.
			sentinel := srv.MustCreateEvent(t, room, federation.Event{
				Type:     spec.MRoomMember,
				Sender:   bob,
				StateKey: &bob,
				Content: map[string]interface{}{
					"membership":  "join",
					"displayname": "SENTINEL",
				},
			})
			room.AddEvent(sentinel)
			eventsToSend = append(eventsToSend, sentinel)
			t.Logf("%s SENTINEL %v", sentinel.EventID(), string(sentinel.JSON()))

			srv.MustSendTransaction(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), AsEventJSONs(eventsToSend), nil)

			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, sentinel.EventID()))

			assertGetMissingEventsRecursively(
				tc.name, room.RoomID, sentinel, tc.getMissingEventsLimit, tc.walkStateDAG, wantEventIDs,
			)
		})
	}
}

func TestMSC4242GetMissingEventsOutbound(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

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

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := srv.UserID("bob")

	var getMissingEventsHandler func(w http.ResponseWriter, req *http.Request)
	srv.Mux().HandleFunc("/_matrix/federation/v1/get_missing_events/{roomID}", func(w http.ResponseWriter, req *http.Request) {
		getMissingEventsHandler(w, req)
	})

	t.Run("GME01A-Outbound: can walk linear graphs", func(t *testing.T) {
		linearRoom := srv.MustMakeRoom(t, roomVersion,
			federation.InitialRoomEvents(roomVersion, bob),
			federation.WithImpl(ServerRoomImplStateDAG(t, srv)),
		)
		alice.MustJoinRoom(t, linearRoom.RoomID, []spec.ServerName{srv.ServerName()})
		// Generate enough state events to cause repeated calls to /get_missing_events to build
		// confidence that the server has implemented recursive walking.
		linearEvents := generateDisplayNameChanges(t, srv, linearRoom, bob, 80, true)
		lastEventInTxn := linearEvents[len(linearEvents)-1]
		prevLastEvent := linearEvents[len(linearEvents)-2]
		getMissingEventsHandler = func(w http.ResponseWriter, req *http.Request) {
			body, err := extractGetMissingEventsRequest(linearRoom.RoomID, req)
			if err != nil {
				ct.Errorf(t, "bad /get_missing_events request, returning %v", err)
				w.WriteHeader(400)
				w.Write([]byte(err.Error()))
				return
			}
			var resp fclient.RespMissingEvents
			if !body.StateDAG {
				// as the event in the txn has unknown prev_events, we will also receive /get_missing_events queries for the normal graph.
				// Just return the penultimate event to keep it happy.
				resp.Events = gomatrixserverlib.EventJSONs{prevLastEvent.JSON()}
				t.Logf("Complement responding to /get_missing_events (normal DAG) with 1 event to keep the server happy")
				w.WriteHeader(200)
				if err := json.NewEncoder(w).Encode(&resp); err != nil {
					ct.Errorf(t, "failed to encode response body: %s", err)
				}
				return
			}
			// we expect a single latest_event (because the graph is linear), which we will use to find the index in linearEvents to start returning events from
			// in reverse chronological order.
			must.Equal(t, len(body.LatestEvents), 1, fmt.Sprintf("expected 1 latest_event, got %v", body.LatestEvents))
			from := body.LatestEvents[0]
			// find the event
			for j, ev := range linearEvents {
				if ev.EventID() == from {
					// return the next batch, honouring the limit.
					i := j - body.Limit
					if i < 0 {
						i = 0
					}
					resp.Events = gomatrixserverlib.NewEventJSONsFromEvents(linearEvents[i:j])
					t.Logf("/get_missing_events for %v returning batch i=%d j=%d", body.LatestEvents, i, j)
					break
				}
			}
			if len(resp.Events) == 0 {
				ct.Errorf(t, "returning 0 events to /get_missing_events (state DAG) for latest_events: %v", body.LatestEvents)
			}
			t.Logf("Complement responding to /get_missing_events (state DAG) with %d events", len(resp.Events))
			w.WriteHeader(200)
			if err := json.NewEncoder(w).Encode(&resp); err != nil {
				ct.Errorf(t, "failed to encode response body: %s", err)
			}
		}

		// send the last event, which should cause the homeserver to ask for earlier events, which we will return in a linear order
		srv.MustSendTransaction(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), []json.RawMessage{
			lastEventInTxn.JSON(),
		}, nil)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(linearRoom.RoomID, lastEventInTxn.EventID()))
	})
	// Generates a DAG like this:
	//             ALICE_JOIN
	//             /    |     \
	//        STATE    STATE  STATE  ..x10
	//          |       |      |
	//        STATE    STATE  STATE  ..x10
	//          |       |       |
	//        STATE    STATE  STATE  ..x10
	//           \      |     /
	//            \     |    /
	//           UNIFYING EVENT
	//                  |
	//           EVENT_IN_SEND_TXN
	//
	// for a total of 30 events (10 on top/middle/bottom layers)
	// with a single unifying event which has 10 prev_state_events. Another event is then spun off
	// the unifying event and sent in the /send txn. We don't use the unifying event here as the event
	// in the /send txn needs to include all prev_events, which would mean we would be forced to send
	// all forks to satisfy that, which isn't the point of the test.
	//
	// The intention of this test is to check that:
	//  - servers which have a low limit (e.g 8 like Synapse) will continue /get_missing_events if the
	//    event has > limit prev_state_events (10 in the case of the unifying event)
	//  - servers set the latest_events correctly. All paths must be walked so all prev_state_events in the unifying evnet
	//    must eventually end up in latest_events.
	//
	// The test will linearise the DAG for responding to /get_missing_events queries as per the MSC.
	t.Run("GME01B-Outbound: can walk forked graphs", func(t *testing.T) {
		numForks := 10
		numEventsPerFork := 3
		forkRoom := srv.MustMakeRoom(t, roomVersion,
			federation.InitialRoomEvents(roomVersion, bob),
			federation.WithImpl(ServerRoomImplStateDAG(t, srv, WithDontCheckForwardExtremities())),
		)
		alice.MustJoinRoom(t, forkRoom.RoomID, []spec.ServerName{srv.ServerName()})
		// TODO: wait a bit to ensure we have the join event. Ideally we'd use WaiterForEvent but that expects
		// us to know the event ID of the join event, which the /join endpoint does not provide :(
		time.Sleep(time.Second)
		aliceJoinEventID := forkRoom.CurrentState(spec.MRoomMember, alice.UserID).EventID()
		// create 10 strands
		var forkEvents []gomatrixserverlib.PDU
		var lastEventsInForks []gomatrixserverlib.PDU
		for forkIndex := 0; forkIndex < numForks; forkIndex++ {
			parentEventID := aliceJoinEventID
			var lastEvent gomatrixserverlib.PDU
			for eventIndex := 0; eventIndex < numEventsPerFork; eventIndex++ {
				forkRoom.ForwardExtremities = []string{parentEventID}
				ev := srv.MustCreateEvent(t, forkRoom, federation.Event{
					Type:     spec.MRoomMember,
					Sender:   bob,
					StateKey: &bob,
					Content: map[string]interface{}{
						"membership":  spec.Join,
						"displayname": fmt.Sprintf("fork strand=%d event=%d", forkIndex, eventIndex),
					},
				})
				parentEventID = ev.EventID()
				forkEvents = append(forkEvents, ev)
				lastEvent = ev
			}
			lastEventsInForks = append(lastEventsInForks, lastEvent)
		}
		forkRoom.ForwardExtremities = AsEventIDs(t, lastEventsInForks)
		unifyingEvent := srv.MustCreateEvent(t, forkRoom, federation.Event{
			Type:     spec.MRoomMember,
			Sender:   bob,
			StateKey: &bob,
			Content: map[string]interface{}{
				"membership":  spec.Join,
				"displayname": "UNIFIER",
			},
		})
		t.Logf("created unifying event %s with prev_state_events = %v", unifyingEvent.EventID(), unifyingEvent.PrevStateEventIDs())
		for _, ev := range forkEvents {
			t.Logf("%s = %s (prev_state_events=%v)", ev.EventID(), gjson.ParseBytes(ev.Content()).Get("displayname").Str, ev.PrevStateEventIDs())
			forkRoom.AddEvent(ev)
		}
		forkRoom.AddEvent(unifyingEvent)
		latestEvent := srv.MustCreateEvent(t, forkRoom, federation.Event{
			Type:     spec.MRoomName,
			Sender:   bob,
			StateKey: &empty,
			Content: map[string]interface{}{
				"name": "LATEST",
			},
		})
		sg := NewStateGraph()
		sg.Update(forkRoom.Timeline)

		getMissingEventsHandler = func(w http.ResponseWriter, req *http.Request) {
			body, err := extractGetMissingEventsRequest(forkRoom.RoomID, req)
			if err != nil {
				ct.Errorf(t, "bad /get_missing_events request, returning %v", err)
				w.WriteHeader(400)
				w.Write([]byte(err.Error()))
				return
			}
			var resp fclient.RespMissingEvents
			if !body.StateDAG {
				// this should be the unifying event, so return the last event in each fork
				if len(body.LatestEvents) == 1 && body.LatestEvents[0] == latestEvent.EventID() {
					resp.Events = gomatrixserverlib.NewEventJSONsFromEvents([]gomatrixserverlib.PDU{unifyingEvent})
					t.Logf("Complement responding to /get_missing_events (normal DAG) with 1 event")
					w.WriteHeader(200)
					if err := json.NewEncoder(w).Encode(&resp); err != nil {
						ct.Errorf(t, "failed to encode response body: %s", err)
					}
					return
				}
				w.WriteHeader(400)
				t.Logf("Complement: unexpected non-state dag /get_missing_events request. Latest=%v", body.LatestEvents)
				return
			}
			t.Logf("/get_missing_events limit=%d latest=%v", body.Limit, body.LatestEvents)
			resp.Events = gomatrixserverlib.NewEventJSONsFromEvents(
				sg.GetMissingEvents(body.LatestEvents, body.Limit),
			)
			t.Logf("Complement responding to /get_missing_events (state dag) with %d events", len(resp.Events))
			w.WriteHeader(200)
			if err := json.NewEncoder(w).Encode(&resp); err != nil {
				ct.Errorf(t, "failed to encode response body: %s", err)
			}
		}

		// send the last event, which should cause the homeserver to ask for earlier events
		srv.MustSendTransaction(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), []json.RawMessage{
			latestEvent.JSON(),
		}, nil)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(forkRoom.RoomID, latestEvent.EventID()))
	})

}

func TestMSC4242GetMissingEventsBadInputs(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

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

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := srv.UserID("bob")

	room := srv.MustMakeRoom(t, roomVersion,
		federation.InitialRoomEvents(roomVersion, bob),
		federation.WithImpl(ServerRoomImplStateDAG(t, srv)),
	)
	alice.MustJoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})
	msg := srv.MustCreateEvent(t, room, federation.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "I am a message",
		},
		Sender: bob,
	})
	room.AddEvent(msg)

	srv.MustSendTransaction(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), AsEventJSONs([]gomatrixserverlib.PDU{msg}), nil)
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(room.RoomID, msg.EventID()))

	testCases := []struct {
		testCode string
		name     string
		req      fclient.MissingEvents
	}{
		{
			testCode: "GME02A",
			name:     "returns nothing for a message event",
			req: fclient.MissingEvents{
				Limit:          5,
				EarliestEvents: []string{},
				LatestEvents:   []string{msg.EventID()},
				StateDAG:       true,
			},
		},
		{
			testCode: "GME02B",
			name:     "returns nothing for a bogus event ID",
			req: fclient.MissingEvents{
				Limit:          5,
				EarliestEvents: []string{},
				LatestEvents:   []string{"$4PRgaFIMcD9z4vzgkUUm0YI5CZHYORUPzWGJac6guAo"},
				StateDAG:       true,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testCode, func(t *testing.T) {
			resp, err := srv.FederationClient(deployment).LookupMissingEvents(
				context.Background(), spec.ServerName(srv.ServerName()), deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
				room.RoomID, tc.req, roomVersion,
			)
			must.NotError(t, "failed to send /gme request", err)
			gotEvents := resp.Events.TrustedEvents(roomVersion, false)
			must.Equal(
				t, len(gotEvents), 0,
				fmt.Sprintf("/get_missing_events returned events for %s: %s", tc.testCode, tc.name),
			)
		})
	}
}

func TestMSC4242GetMissingEventsFaultyEvents(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

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

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := srv.UserID("bob")
	charlie := srv.UserID("charlie")
	doris := srv.UserID("doris")

	var pl gomatrixserverlib.PowerLevelContent
	pl.Defaults()
	pl.Users = map[string]int64{
		doris: 50,
	}
	pl.Events = map[string]int64{
		"m.room.name":               50,
		"m.room.power_levels":       100,
		"m.room.history_visibility": 100,
	}
	plBytes, _ := json.Marshal(pl)
	var plContentMap map[string]interface{}
	json.Unmarshal(plBytes, &plContentMap)
	room := srv.MustMakeRoom(t, roomVersion,
		[]federation.Event{
			{
				Type:     spec.MRoomCreate,
				StateKey: &empty,
				Sender:   bob,
				Content: map[string]interface{}{
					"creator":      bob,
					"room_version": roomVersion,
				},
			},
			{
				Type:     spec.MRoomMember,
				StateKey: &bob,
				Sender:   bob,
				Content: map[string]interface{}{
					"membership": "join",
				},
			},
			{
				Type:     spec.MRoomPowerLevels,
				StateKey: &empty,
				Sender:   bob,
				Content:  plContentMap,
			},
			{
				Type:     spec.MRoomJoinRules,
				StateKey: &empty,
				Sender:   bob,
				Content: map[string]interface{}{
					"join_rule": spec.Public,
				},
			},
		},
		federation.WithImpl(ServerRoomImplStateDAG(t, srv)),
	)
	alice.MustJoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})
	// Create the following events (from Alice's join, Doris has moderator rights already)
	//       ALICE_JOIN
	//     /            \
	//    /              \
	// CHARLIE          DORIS_JOIN
	// SET_ROOM_NAME       (ok)
	//  (rejected)             |
	//   |                _____|______
	// BOB_PROFILE      /              \
	//  (rejected)     /                \
	//   |            /                  \
	//   |           /                    \
	//   |      BOB_BAN_DORIS            DORIS_SET_ROOM_NAME
	//   |         (ok)                      (rollback)
	//   |            \                   /
	//   |             \                 /
	//   |              BOB_SET_TOPIC (ok)
	//   |                  /
	//   |                 /
	//   BOB_SET_ROOM_NAME
	//    (rejected, a prev_state_event is rejected)
	// CHARLIE SET_ROOM_NAME is rejected because charlie didn't join.
	// BOB_PROFILE is rejected because it is hanging off a rejected event.
	// DORIS_SET_ROOM_NAME state rollbacks because there is a concurrent ban.
	aliceJoin := room.CurrentState(spec.MRoomMember, alice.UserID)
	t.Logf("aliceJoin => %v", aliceJoin.EventID())
	charlieSetRoomName := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:     spec.MRoomName,
			Sender:   charlie,
			StateKey: &empty,
			Content: map[string]interface{}{
				"name": "Charlie didn't join so cannot set the room name",
			},
			PrevEvents: []string{aliceJoin.EventID()},
		},
		PrevStateEvents: []string{aliceJoin.EventID()},
	})
	t.Logf("charlieSetRoomName (REJECTED) => %v prev_state_events=%v", charlieSetRoomName.EventID(), charlieSetRoomName.PrevStateEventIDs())
	dorisJoin := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:     spec.MRoomMember,
			Sender:   doris,
			StateKey: &doris,
			Content: map[string]interface{}{
				"membership": spec.Join,
			},
			PrevEvents: []string{aliceJoin.EventID()},
		},
		PrevStateEvents: []string{aliceJoin.EventID()},
	})
	t.Logf("dorisJoin => %v prev_state_events=%v", dorisJoin.EventID(), dorisJoin.PrevStateEventIDs())
	bobProfile := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:     spec.MRoomMember,
			Sender:   bob,
			StateKey: &bob,
			Content: map[string]interface{}{
				"membership":  spec.Join,
				"displayname": "Bob changed his display name",
			},
			PrevEvents: []string{charlieSetRoomName.EventID()},
		},
		PrevStateEvents: []string{charlieSetRoomName.EventID()},
	})
	t.Logf("bobProfile (prev_state_event is REJECTED) => %v prev_state_events=%v", bobProfile.EventID(), bobProfile.PrevStateEventIDs())
	bobBanDoris := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:     spec.MRoomMember,
			Sender:   bob,
			StateKey: &doris,
			Content: map[string]interface{}{
				"membership": spec.Ban,
			},
			PrevEvents: []string{dorisJoin.EventID()},
		},
		PrevStateEvents: []string{dorisJoin.EventID()},
	})
	t.Logf("bobBanDoris => %v prev_state_events=%v", bobBanDoris.EventID(), bobBanDoris.PrevStateEventIDs())
	dorisSetRoomName := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:     spec.MRoomName,
			Sender:   doris,
			StateKey: &empty,
			Content: map[string]interface{}{
				"name": "Doris is concurrently banned when setting the display name",
			},
			PrevEvents: []string{dorisJoin.EventID()},
		},
		PrevStateEvents: []string{dorisJoin.EventID()},
	})
	t.Logf("dorisSetRoomName (soft-fail) => %v prev_state_events=%v", dorisSetRoomName.EventID(), dorisSetRoomName.PrevStateEventIDs())
	bobSetTopic := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:     spec.MRoomTopic,
			Sender:   bob,
			StateKey: &empty,
			Content: map[string]interface{}{
				"topic": "Bob set the topic",
			},
			PrevEvents: []string{bobBanDoris.EventID(), dorisSetRoomName.EventID()},
		},
		PrevStateEvents: []string{bobBanDoris.EventID(), dorisSetRoomName.EventID()},
	})
	t.Logf("bobSetTopic => %v prev_state_events=%v", bobSetTopic.EventID(), bobSetTopic.PrevStateEventIDs())
	bobSetRoomName := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:     spec.MRoomName,
			Sender:   bob,
			StateKey: &empty,
			Content: map[string]interface{}{
				"name": "Bob reffs a rejected event when setting the room name",
			},
			PrevEvents: []string{bobProfile.EventID(), bobSetTopic.EventID()},
		},
		PrevStateEvents: []string{bobProfile.EventID(), bobSetTopic.EventID()},
	})
	t.Logf(
		"bobSetRoomName (REJECTED, 1 prev_state_event is rejected) => %v prev_state_events=%v",
		bobSetRoomName.EventID(), bobSetRoomName.PrevStateEventIDs(),
	)

	// we will send the room name change later on, just to make sure the ban has been applied first.
	firstBatch := []gomatrixserverlib.PDU{
		charlieSetRoomName, dorisJoin, bobProfile, bobBanDoris,
	}
	srv.MustSendTransaction(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), AsEventJSONs(firstBatch), nil)
	since := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(room.RoomID, bobBanDoris.EventID()))
	// now send the rest
	secondBatch := []gomatrixserverlib.PDU{
		dorisSetRoomName, bobSetTopic, bobSetRoomName,
	}
	srv.MustSendTransaction(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), AsEventJSONs(secondBatch), nil)
	alice.MustSyncUntil(t, client.SyncReq{Since: since}, client.SyncTimelineHasEventID(room.RoomID, bobSetTopic.EventID()))

	// /get_missing_events strictly defines the ordering walked when there are multiple prev_state_events, and it
	// depends on the event ID so figure that out now.
	concurrentOrdering := []string{
		dorisSetRoomName.EventID(), bobBanDoris.EventID(),
	}
	if bobBanDoris.EventID() < dorisSetRoomName.EventID() {
		concurrentOrdering = []string{
			bobBanDoris.EventID(), dorisSetRoomName.EventID(),
		}
	}

	testCases := []struct {
		testCode     string
		name         string
		req          fclient.MissingEvents
		wantEventIDs []string
	}{
		{
			testCode: "GME03A",
			name:     "includes soft-failed events when walking",
			req: fclient.MissingEvents{
				Limit:          4,
				EarliestEvents: []string{},
				LatestEvents:   []string{bobSetTopic.EventID()},
				StateDAG:       true,
			},
			wantEventIDs: append(concurrentOrdering, dorisJoin.EventID(), aliceJoin.EventID()),
		},

		{
			testCode: "GME03B",
			name:     "returns nothing for a rejected event ID",
			req: fclient.MissingEvents{
				Limit:          5,
				EarliestEvents: []string{},
				LatestEvents:   []string{charlieSetRoomName.EventID()},
				StateDAG:       true,
			},
			wantEventIDs: []string{},
		},

		{
			testCode: "GME03C",
			name:     "returns nothing for a valid event which points to a rejected event ID",
			req: fclient.MissingEvents{
				Limit:          5,
				EarliestEvents: []string{},
				LatestEvents:   []string{bobProfile.EventID()},
				StateDAG:       true,
			},
			wantEventIDs: []string{},
		},
		{
			testCode: "GME03D",
			name:     "returns nothing for a valid event which points to multiple events, one of which is a rejected event ID",
			req: fclient.MissingEvents{
				Limit:          5,
				EarliestEvents: []string{},
				LatestEvents:   []string{bobSetRoomName.EventID()},
				StateDAG:       true,
			},
			wantEventIDs: []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testCode, func(t *testing.T) {
			resp, err := srv.FederationClient(deployment).LookupMissingEvents(
				context.Background(), spec.ServerName(srv.ServerName()), deployment.GetFullyQualifiedHomeserverName(t, "hs1"), room.RoomID, tc.req, roomVersion,
			)
			must.NotError(t, "failed to send /gme request", err)
			gotEvents := resp.Events.TrustedEvents(roomVersion, false)

			gotEventIDs := AsEventIDs(t, gotEvents)
			if !slices.Equal(gotEventIDs, tc.wantEventIDs) {
				ct.Errorf(t, "/get_missing_events returned events mismatch:\ngot  %v\nwant %v", gotEventIDs, tc.wantEventIDs)
			}
		})
	}
}

// GME05: when filling in the state DAG, it fails the incoming /send event if /get_missing_events returns:
//
//	A: no events
//	B: bogus events (e.g not events referenced in prev_state_events)
//	C: partial events (e.g 2 prev state events and only send the same 1)
//	D: an HTTP error
func TestMSC4242GetMissingEventsFillingStateDAGFails(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

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

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := srv.UserID("bob")

	var getMissingEventsHandler func(w http.ResponseWriter, req *http.Request)
	srv.Mux().HandleFunc("/_matrix/federation/v1/get_missing_events/{roomID}", func(w http.ResponseWriter, req *http.Request) {
		getMissingEventsHandler(w, req)
	})

	testCases := []struct {
		testCode           string
		name               string
		seeLastEvent       bool
		onGetMissingEvents func(room *federation.ServerRoom, req *fclient.MissingEvents) (statusCode int, resp *fclient.RespMissingEvents)
	}{
		{
			testCode: "GME05A",
			name:     "no events",
			onGetMissingEvents: func(room *federation.ServerRoom, req *fclient.MissingEvents) (statusCode int, resp *fclient.RespMissingEvents) {
				var r fclient.RespMissingEvents
				r.Events = []spec.RawJSON{}
				return 200, &r
			},
		},
		{
			testCode: "GME05B",
			name:     "bogus events",
			onGetMissingEvents: func(room *federation.ServerRoom, req *fclient.MissingEvents) (statusCode int, resp *fclient.RespMissingEvents) {
				var r fclient.RespMissingEvents
				// return the create event instead
				r.Events = gomatrixserverlib.NewEventJSONsFromEvents(
					[]gomatrixserverlib.PDU{room.CurrentState(spec.MRoomCreate, "")},
				)
				return 200, &r
			},
		},
		{
			testCode: "GME05C",
			name:     "partial events",
			onGetMissingEvents: func(room *federation.ServerRoom, req *fclient.MissingEvents) (statusCode int, resp *fclient.RespMissingEvents) {
				var r fclient.RespMissingEvents
				// always return C, when we need B _and_ C
				r.Events = gomatrixserverlib.NewEventJSONsFromEvents(
					[]gomatrixserverlib.PDU{room.CurrentState("m.event.C", "")},
				)
				return 200, &r
			},
		},
		{
			testCode:     "sanity check",
			name:         "ensure that if we do actually send the right events back, we do see the latest event",
			seeLastEvent: true,
			onGetMissingEvents: func(room *federation.ServerRoom, req *fclient.MissingEvents) (statusCode int, resp *fclient.RespMissingEvents) {
				var r fclient.RespMissingEvents
				// return everything, but with a HTTP 500 status code so we should fail the event.
				r.Events = gomatrixserverlib.NewEventJSONsFromEvents(
					[]gomatrixserverlib.PDU{
						room.CurrentState("m.event.A", ""),
						room.CurrentState("m.event.B", ""),
						room.CurrentState("m.event.C", ""),
					},
				)
				return 200, &r
			},
		},
		{
			testCode: "GME05D",
			name:     "HTTP error",
			onGetMissingEvents: func(room *federation.ServerRoom, req *fclient.MissingEvents) (statusCode int, resp *fclient.RespMissingEvents) {
				var r fclient.RespMissingEvents
				// return everything, but with a HTTP 500 status code so we should fail the event.
				r.Events = gomatrixserverlib.NewEventJSONsFromEvents(
					[]gomatrixserverlib.PDU{
						room.CurrentState("m.event.A", ""),
						room.CurrentState("m.event.B", ""),
						room.CurrentState("m.event.C", ""),
					},
				)
				return 500, &r
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.testCode, func(t *testing.T) {
			room := srv.MustMakeRoom(t, roomVersion,
				federation.InitialRoomEvents(roomVersion, bob),
				federation.WithImpl(ServerRoomImplStateDAG(t, srv)),
			)
			alice.MustJoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})
			// this event will be accepted as the server knows the prev_state_events (it's alice's join)
			sentinel := srv.MustCreateEvent(t, room, federation.Event{
				Type:     "m.room.name",
				StateKey: &empty,
				Sender:   bob,
				Content: map[string]interface{}{
					"name": "sentinel",
				},
			})
			t.Logf("alice's join event: %v", room.CurrentState(spec.MRoomMember, alice.UserID).EventID())
			t.Logf("sentinel event: %v (prev_state_events=%v)", sentinel.EventID(), sentinel.PrevStateEventIDs())

			// generate a simple fork
			//      A
			//     / \
			//    B   C
			//     \ /
			//      D  <-- return in /get_missing_events
			//      |
			//      E  <-- send in /send
			eventA := srv.MustCreateEvent(t, room, federation.Event{
				Type:     "m.event.A",
				StateKey: &empty,
				Sender:   bob,
				Content: map[string]interface{}{
					"event": "A",
				},
			})
			room.AddEvent(eventA)
			eventB := srv.MustCreateEvent(t, room, federation.Event{
				Type:     "m.event.B",
				StateKey: &empty,
				Sender:   bob,
				Content: map[string]interface{}{
					"event": "B",
				},
			})
			eventC := srv.MustCreateEvent(t, room, federation.Event{
				Type:     "m.event.C",
				StateKey: &empty,
				Sender:   bob,
				Content: map[string]interface{}{
					"event": "C",
				},
			})
			room.AddEvent(eventB)
			room.AddEvent(eventC)
			room.ForwardExtremities = []string{eventB.EventID(), eventC.EventID()}
			eventD := srv.MustCreateEvent(t, room, federation.Event{
				Type:     "m.event.D",
				StateKey: &empty,
				Sender:   bob,
				Content: map[string]interface{}{
					"event": "D",
				},
			})
			room.AddEvent(eventD)
			eventE := srv.MustCreateEvent(t, room, federation.Event{
				Type:     "m.event.E",
				StateKey: &empty,
				Sender:   bob,
				Content: map[string]interface{}{
					"event": "E",
				},
			})
			room.AddEvent(eventE)
			generatedEvents := []gomatrixserverlib.PDU{
				eventA, eventB, eventC, eventD, eventE,
			}
			generatedEventLabels := []string{"A", "B", "C", "D", "E"}
			for i, ev := range generatedEvents {
				t.Logf("Event %s: %s %s (prev_state_events=%v)", generatedEventLabels[i], ev.Type(), ev.EventID(), ev.PrevStateEventIDs())
			}

			var hitGetMissingEvents atomic.Bool
			// intercept /get_missing_events and return D for non-state DAG requests, forwarding the rest to the test case
			getMissingEventsHandler = func(w http.ResponseWriter, req *http.Request) {
				body, err := extractGetMissingEventsRequest(room.RoomID, req)
				if err != nil {
					ct.Errorf(t, "bad /get_missing_events request, returning %v", err)
					w.WriteHeader(400)
					w.Write([]byte(err.Error()))
					return
				}
				if !body.StateDAG {
					must.Equal(t, slices.Equal(body.LatestEvents, []string{eventE.EventID()}), true, fmt.Sprintf(
						"unexpected latest events (expected event E) for non-state dag request: %v", body.LatestEvents,
					))
					var resp fclient.RespMissingEvents
					resp.Events = gomatrixserverlib.NewEventJSONsFromEvents([]gomatrixserverlib.PDU{
						eventD,
					})
					w.WriteHeader(200)
					if err := json.NewEncoder(w).Encode(&resp); err != nil {
						ct.Errorf(t, "failed to encode /get_missing_events response: %v", err)
					}
					return
				}
				must.Equal(t, slices.Equal(body.LatestEvents, []string{eventD.EventID()}), true, fmt.Sprintf(
					"unexpected latest events (expected event D) for non-state dag request: %v", body.LatestEvents,
				))
				// defer to the test case
				hitGetMissingEvents.Store(true)
				statusCode, resp := tc.onGetMissingEvents(room, body)
				w.WriteHeader(statusCode)
				if err := json.NewEncoder(w).Encode(&resp); err != nil {
					ct.Errorf(t, "failed to encode /get_missing_events response: %v", err)
				}
			}
			// send E
			srv.MustSendTransaction(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), []json.RawMessage{eventE.JSON()}, nil)

			// send the sentinel to confirm it has processed the event which should have been rejected.
			srv.MustSendTransaction(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), []json.RawMessage{sentinel.JSON()}, nil)
			hasSentinel := client.SyncTimelineHasEventID(room.RoomID, sentinel.EventID())
			hasLastEvent := client.SyncTimelineHasEventID(room.RoomID, eventE.EventID())
			alice.MustSyncUntil(t, client.SyncReq{}, func(clientUserID string, topLevelSyncJSON gjson.Result) error {
				if err := hasSentinel(clientUserID, topLevelSyncJSON); err == nil {
					return nil
				}
				err := hasLastEvent(clientUserID, topLevelSyncJSON)
				if err == nil {
					if tc.seeLastEvent {
						return nil // it was expected to see the last event
					}
					ct.Errorf(t, "timeline has event that should have been rejected: %v", topLevelSyncJSON.Raw)
				}
				return err
			})
			if !hitGetMissingEvents.Load() {
				ct.Errorf(t, "server never hit /get_missing_events")
			}
		})
	}

}
