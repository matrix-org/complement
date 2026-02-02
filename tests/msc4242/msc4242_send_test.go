package tests

import (
	"fmt"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/tidwall/gjson"
)

// SEND: /send tests
//  Note: these tests mirror some of the ones in /send_join as it's all doing event validation.
//  SEND00: sending a faulty state event doesn't brick the room (subsequent valid events work)
//   See SJ02 for the full rejected list
//  SEND01: a banned user cannot evade a ban and have their message or state event be sent down /sync by:
//   A: referencing prev_state_events from after they were banned, and prev_events after they were banned.
//   B: referencing prev_state_events from when they were joined, but prev_events after they were banned.
//   C: referencing prev_state_events from after they were banned, but prev_events from when they were joined.
//   D: referencing prev_state_events from when they were joined, and prev_events from when they were joined.

func TestMSC4242SendingFaultyEventDoesntBrickRoom(t *testing.T) {
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

	testCases := faultyEventTestCases
	for _, tc := range testCases {
		t.Run("SEND00"+tc.CodeSuffix, func(t *testing.T) {
			room := srv.MustMakeRoom(t, roomVersion,
				federation.InitialRoomEvents(roomVersion, bob),
				federation.WithImpl(ServerRoomImplStateDAG(t, srv)),
			)
			alice.MustJoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})
			since := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, room.RoomID))
			faultyEvents := tc.GenerateEvents(t, srv, room, bob)

			srv.MustSendTransaction(t, deployment, "hs1", AsEventJSONs(faultyEvents), nil)

			// send another txn with a sentinel to know that the faulty events have been processed, and
			// ensure we pin it on an event which we know what not rejected (Alice's join)
			room.ForwardExtremities = []string{
				room.CurrentState(spec.MRoomMember, alice.UserID).EventID(),
			}
			sentinel := srv.MustCreateEvent(t, room, federation.Event{
				Type:     "m.room.name",
				StateKey: &empty,
				Sender:   bob,
				Content: map[string]interface{}{
					"name": "sentinel",
				},
			})
			srv.MustSendTransaction(t, deployment, "hs1", AsEventJSONs([]gomatrixserverlib.PDU{sentinel}), nil)

			// we sync incrementally to know that incremental syncs don't break
			alice.MustSyncUntil(t, client.SyncReq{Since: since}, client.SyncTimelineHasEventID(room.RoomID, sentinel.EventID()))
			// and also do an initial sync to make sure that works fine.
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(room.RoomID, sentinel.EventID()))
		})
	}
}

func TestMSC4242BanEvasion(t *testing.T) {
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
	charlie := srv.UserID("charlie")
	// Alice creates a room. Bob and Charlie join.
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"room_version": roomVersion,
		"preset":       "public_chat",
	})
	room := srv.MustJoinRoom(t, deployment, "hs1", roomID, bob, federation.WithRoomOpts(federation.WithImpl(ServerRoomImplStateDAG(t, srv))))
	charlieJoinEvent := srv.MustCreateEvent(t, room, federation.Event{
		Type:     spec.MRoomMember,
		StateKey: &charlie,
		Sender:   charlie,
		Content: map[string]interface{}{
			"membership": "join",
		},
	})
	room.AddEvent(charlieJoinEvent)
	srv.MustSendTransaction(t, deployment, "hs1", AsEventJSONs([]gomatrixserverlib.PDU{
		charlieJoinEvent,
	}), nil)
	since := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob, roomID), client.SyncJoinedTo(charlie, roomID))
	bobsJoinEventID := room.CurrentState(spec.MRoomMember, bob).EventID()

	// Ban Bob.
	alice.MustDo(t, "POST", []string{
		"_matrix", "client", "v3", "rooms", roomID, "ban",
	}, client.WithJSONBody(t, map[string]any{
		"user_id": bob,
	}))

	var banEventID string
	since = alice.MustSyncUntil(t, client.SyncReq{Since: since}, client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		isBanEvent := r.Get("type").Str == spec.MRoomMember && r.Get("state_key").Str == bob && r.Get("content.membership").Str == "ban"
		if isBanEvent {
			banEventID = r.Get("event_id").Str
		}
		return isBanEvent
	}))
	t.Logf("ban event ID: %v", banEventID)

	testCases := []struct {
		testCode       string
		name           string
		configureEvent func(stub *MSC4242Event)
	}{
		{
			testCode: "SEND01A",
			name:     "prev_events and prev_state_events after ban",
			configureEvent: func(stub *MSC4242Event) {
				stub.PrevStateEvents = []string{banEventID}
				stub.PrevEvents = []string{banEventID}
			},
		},
		{
			testCode: "SEND01B",
			name:     "prev_events after ban, prev_state_events before ban",
			configureEvent: func(stub *MSC4242Event) {
				stub.PrevStateEvents = []string{bobsJoinEventID}
				stub.PrevEvents = []string{banEventID}
			},
		},
		{
			testCode: "SEND01C",
			name:     "prev_events before ban, prev_state_events after ban",
			configureEvent: func(stub *MSC4242Event) {
				stub.PrevStateEvents = []string{banEventID}
				stub.PrevEvents = []string{bobsJoinEventID}
			},
		},
		{
			testCode: "SEND01D",
			name:     "prev_events before ban, prev_state_events before ban",
			configureEvent: func(stub *MSC4242Event) {
				stub.PrevStateEvents = []string{bobsJoinEventID}
				stub.PrevEvents = []string{bobsJoinEventID}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.testCode, func(t *testing.T) {
			msg := MSC4242Event{
				Event: federation.Event{
					Type:   "m.room.message",
					Sender: bob,
					Content: map[string]interface{}{
						"msgtype": "m.text",
						"body":    "Can I evade bans?",
					},
				},
			}
			tc.configureEvent(&msg)
			msgPDU := mustCreateEvent(t, srv, room, msg)
			state := MSC4242Event{
				Event: federation.Event{
					Type:     spec.MRoomMember,
					Sender:   bob,
					StateKey: &bob,
					Content: map[string]interface{}{
						"membership":  "join",
						"displayname": "State",
					},
				},
			}
			tc.configureEvent(&state)
			statePDU := mustCreateEvent(t, srv, room, state)
			sentinel := srv.MustCreateEvent(t, room, federation.Event{
				Type:   "m.room.message",
				Sender: charlie,
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    "Sentinel check",
				},
			})
			srv.MustSendTransaction(t, deployment, "hs1", AsEventJSONs([]gomatrixserverlib.PDU{msgPDU, statePDU, sentinel}), nil)
			t.Logf("msg=%v state=%v sentinel=%v for test '%s'", msgPDU.EventID(), statePDU.EventID(), sentinel.EventID(), tc.name)
			// collect all events down /sync until we see the sentinel
			seenEvents := make(map[string]bool)
			since = alice.MustSyncUntil(t, client.SyncReq{Since: since},
				func(clientUserID string, topLevelSyncJSON gjson.Result) error {
					client.SyncStateHas(roomID, func(r gjson.Result) bool {
						seenEvents[r.Get("event_id").Str] = true
						return false
					})(clientUserID, topLevelSyncJSON)
					client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
						seenEvents[r.Get("event_id").Str] = true
						return false
					})(clientUserID, topLevelSyncJSON)
					if seenEvents[sentinel.EventID()] {
						return nil
					}
					return fmt.Errorf("haven't seen sentinel event")
				},
			)
			if seenEvents[statePDU.EventID()] {
				ct.Errorf(t, "%s: saw state event from banned user: %v", tc.name, statePDU.EventID())
			}
			if seenEvents[msgPDU.EventID()] {
				ct.Errorf(t, "%s: saw msg event from banned user: %v", tc.name, msgPDU.EventID())
			}
		})
	}

}
