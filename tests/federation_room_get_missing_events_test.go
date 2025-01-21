package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/matrix-org/util"
	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

// TODO:
// Outbound federation can request missing events
// outliers whose auth_events are in a different room are correctly rejected

// /get_missing_events is used to fill in gaps in the room DAG when a server is pushed (via /send)
// an event with unknown prev_events. A gap can be "filled" if there is an overlap between the events
// from /get_missing_events and what the server already knows. In the event that a gap is filled,
// the server should deliver all missed messages to the client and critically, NOT do any further
// requests like /state or /state_ids. This test exists as a Dendrite regression test where a change to
// /get_missing_events resulted in gaps never being filled so Dendrite would ALWAYS hit /state_ids
// even when it knew the earliest events. This test doesn't fork the DAG in any way, it's entirely
// linear.
func TestGetMissingEventsGapFilling(t *testing.T) {
	// 1) Create a room between the HS and Complement.
	// 2) Inject events into Complement but don't deliver them to the HS.
	// 3) Inject a final event into Complement and send that alone to the HS.
	// 4) Respond to /get_missing_events with the missing events if the request is well-formed.
	// 5) Ensure the HS doesn't do /state_ids or /state
	// 6) Ensure Alice sees all injected events in the correct order.
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(nil, nil),
	)
	// 5) Ensure the HS doesn't do /state_ids or /state
	srv.Mux().HandleFunc("/_matrix/federation/v1/state/{roomID}", func(w http.ResponseWriter, req *http.Request) {
		t.Errorf("Received request to /_matrix/federation/v1/state/{roomID}")
	}).Methods("GET")
	srv.Mux().HandleFunc("/_matrix/federation/v1/state_ids/{roomID}", func(w http.ResponseWriter, req *http.Request) {
		t.Errorf("Received request to /_matrix/federation/v1/state_ids/{roomID}")
	}).Methods("GET")
	cancel := srv.Listen()
	defer cancel()

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := srv.UserID("bob")

	// 1) Create a room between the HS and Complement.
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})
	srvRoom := srv.MustJoinRoom(t, deployment, "hs1", roomID, bob)
	lastSharedEvent := srvRoom.Timeline[len(srvRoom.Timeline)-1]

	// 2) Inject events into Complement but don't deliver them to the HS.
	var missingEvents []json.RawMessage
	var missingEventIDs []string
	numMissingEvents := 5
	for i := 0; i < numMissingEvents; i++ {
		missingEvent := srv.MustCreateEvent(t, srvRoom, federation.Event{
			Sender: bob,
			Type:   "m.room.message",
			Content: map[string]interface{}{
				"body": fmt.Sprintf("Missing event %d/%d", i+1, numMissingEvents),
			},
		})
		srvRoom.AddEvent(missingEvent)
		missingEvents = append(missingEvents, missingEvent.JSON())
		missingEventIDs = append(missingEventIDs, missingEvent.EventID())
	}

	// 3) Inject a final event into Complement
	mostRecentEvent := srv.MustCreateEvent(t, srvRoom, federation.Event{
		Sender: bob,
		Type:   "m.room.message",
		Content: map[string]interface{}{
			"body": "most recent event",
		},
	})
	srvRoom.AddEvent(mostRecentEvent)

	// 4) Respond to /get_missing_events with the missing events if the request is well-formed.
	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/get_missing_events/{roomID}",
		srv.ValidFederationRequest(t, func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
			if pathParams["roomID"] != roomID {
				t.Errorf("Received /get_missing_events for the wrong room: %s", roomID)
				return util.JSONResponse{
					Code: 400,
					JSON: "wrong room",
				}
			}
			must.MatchFederationRequest(t, fr,
				match.JSONKeyEqual("earliest_events", []interface{}{lastSharedEvent.EventID()}),
				match.JSONKeyEqual("latest_events", []interface{}{mostRecentEvent.EventID()}),
			)
			t.Logf(
				"/get_missing_events request well-formed, sending back response, earliest_events=%v latest_events=%v",
				lastSharedEvent.EventID(), mostRecentEvent.EventID(),
			)
			return util.JSONResponse{
				Code: 200,
				JSON: map[string]interface{}{
					"events": missingEvents,
				},
			}
		}),
	).Methods("POST")

	// 3) ...and send that alone to the HS.
	srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{mostRecentEvent.JSON()}, nil)

	// 6) Ensure Alice sees all injected events in the correct order.
	correctOrderEventIDs := append([]string{lastSharedEvent.EventID()}, missingEventIDs...)
	correctOrderEventIDs = append(correctOrderEventIDs, mostRecentEvent.EventID())
	startedGettingEvents := false
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		next := correctOrderEventIDs[0]
		if r.Get("event_id").Str == next {
			startedGettingEvents = true
			correctOrderEventIDs = correctOrderEventIDs[1:]
			if len(correctOrderEventIDs) == 0 {
				return true
			}
		} else if startedGettingEvents {
			// once we start reading the events we want we should not see any other events to ensure
			// that the order is correct
			t.Errorf("Expected timeline event %s but got %s", next, r.Get("event_id").Str)
		}
		return false
	}))
	if len(correctOrderEventIDs) != 0 {
		t.Errorf("missed some event IDs : %v", correctOrderEventIDs)
	}
}

// A homeserver receiving a response from `get_missing_events` for a version 6
// room with a bad JSON value (e.g. a float) should discard the bad data.
//
// To test this we need to:
//   - Add an event with "bad" data into the room history, but don't send it.
//   - Add a "good" event into the room history and send it.
//   - wait for the homeserver to attempt to get the missing event (with the bad data).
//     (The homeserver should reject the "good" event.)
//   - To check the good event was rejected, send another valid event pointing at
//     the first "good" event, and wait for a call to `/get_missing_events` for
//     that event (thus proving that the homeserver rejected the good event).
//
// sytest: Outbound federation will ignore a missing event with bad JSON for room version 6
func TestOutboundFederationIgnoresMissingEventWithBadJSONForRoomVersion6(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		// Handle any transactions that the homeserver may send when connecting to another homeserver (such as presence)
		federation.HandleTransactionRequests(nil, nil),
	)
	cancel := srv.Listen()
	defer cancel()

	// register a handler for /get_missing_events, via a shim so that we can
	// behave differently as the test progresses.
	var onGetMissingEvents func(w http.ResponseWriter, req *http.Request)
	srv.Mux().HandleFunc("/_matrix/federation/v1/get_missing_events/{roomID}", func(w http.ResponseWriter, req *http.Request) {
		onGetMissingEvents(w, req)
	}).Methods("POST")

	ver := alice.GetDefaultRoomVersion(t)
	charlie := srv.UserID("charlie")
	room := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, charlie))
	roomAlias := srv.MakeAliasMapping("flibble", room.RoomID)
	// join the room
	alice.MustJoinRoom(t, roomAlias, nil)

	latestEvent := room.Timeline[len(room.Timeline)-1]

	// Sign this bad event which has a float (we can't use helpers here as they check it isn't bad)
	badEvent := b.Event{
		Type:   "m.room.message",
		Sender: charlie,
		Content: map[string]interface{}{
			"body":    "Message 1",
			"bad_val": 1.1,
		},
	}
	content, err := json.Marshal(badEvent.Content)
	if err != nil {
		t.Fatalf("failed to marshal badEvent content %+v", badEvent.Content)
	}

	proto := gomatrixserverlib.ProtoEvent{
		SenderID:   badEvent.Sender,
		Depth:      int64(room.Depth + 1), // depth starts at 1
		Type:       badEvent.Type,
		StateKey:   badEvent.StateKey,
		Content:    content,
		RoomID:     room.RoomID,
		PrevEvents: room.ForwardExtremities,
	}
	var stateNeeded gomatrixserverlib.StateNeeded
	stateNeeded, err = gomatrixserverlib.StateNeededForProtoEvent(&proto)
	if err != nil {
		t.Fatalf("failed to create event: failed to work out auth_events : %s", err)
	}
	proto.AuthEvents = room.AuthEvents(stateNeeded)

	// we have to create this event as a v5 event which doesn't assert floats yet
	verImpl, err := gomatrixserverlib.GetRoomVersion(gomatrixserverlib.RoomVersionV5)
	if err != nil {
		t.Fatalf("failed to create event: invalid room version: %s", err)
	}
	eb := verImpl.NewEventBuilderFromProtoEvent(&proto)
	signedBadEvent, err := eb.Build(time.Now(), spec.ServerName(srv.ServerName()), srv.KeyID, srv.Priv)
	if err != nil {
		t.Fatalf("failed to sign event: %s", err)
	}
	room.AddEvent(signedBadEvent)

	// send the first "good" event, referencing the broken event as a prev_event
	sentEvent := srv.MustCreateEvent(t, room, federation.Event{
		Type:   "m.room.message",
		Sender: charlie,
		Content: map[string]interface{}{
			"body": "Message 2",
		},
	})
	room.AddEvent(sentEvent)

	waiter := helpers.NewWaiter()
	onGetMissingEvents = func(w http.ResponseWriter, req *http.Request) {
		defer waiter.Finish()
		must.MatchRequest(t, req, match.HTTPRequest{
			JSON: []match.JSON{
				match.JSONKeyEqual("earliest_events", []interface{}{latestEvent.EventID()}),
				match.JSONKeyEqual("latest_events", []interface{}{sentEvent.EventID()}),
			},
		})
		// return the bad event, which should result in the transaction failing.
		w.WriteHeader(200)
		res := struct {
			Events []gomatrixserverlib.PDU `json:"events"`
		}{
			Events: []gomatrixserverlib.PDU{signedBadEvent},
		}
		var responseBytes []byte
		responseBytes, err = json.Marshal(&res)
		must.NotError(t, "failed to marshal response", err)
		w.Write(responseBytes)
	}

	fedClient := srv.FederationClient(deployment)
	resp, err := fedClient.SendTransaction(context.Background(), gomatrixserverlib.Transaction{
		TransactionID: "wut",
		Origin:        spec.ServerName(srv.ServerName()),
		Destination:   spec.ServerName("hs1"),
		PDUs: []json.RawMessage{
			sentEvent.JSON(),
		},
	})
	waiter.Wait(t, 5*time.Second)
	must.NotError(t, "SendTransaction errored", err)
	if len(resp.PDUs) != 1 {
		t.Fatalf("got %d errors, want 1", len(resp.PDUs))
	}
	_, ok := resp.PDUs[sentEvent.EventID()]
	if !ok {
		t.Fatalf("wrong PDU returned from send transaction, got %v want %s", resp.PDUs, sentEvent.EventID())
	}

	// older versions of Synapse returned an error for the 'good' PDU; nowadays
	// it just ignores it, so we need to send another event referring to the
	// first one and check that we get a /get_missing_events request.

	message3 := srv.MustCreateEvent(t, room, federation.Event{
		Type:   "m.room.message",
		Sender: charlie,
		Content: map[string]interface{}{
			"body": "Message 3",
		},
	})
	room.AddEvent(message3)

	waiter = helpers.NewWaiter()
	onGetMissingEvents = func(w http.ResponseWriter, req *http.Request) {
		must.MatchRequest(t, req, match.HTTPRequest{
			JSON: []match.JSON{
				match.JSONKeyEqual("earliest_events", []interface{}{latestEvent.EventID()}),
				match.JSONKeyEqual("latest_events", []interface{}{message3.EventID()}),
			},
		})
		defer waiter.Finish()

		// we don't really care what we return here, so just return an empty body.
		w.WriteHeader(200)
		w.Write([]byte("{}"))
	}

	resp, err = fedClient.SendTransaction(context.Background(), gomatrixserverlib.Transaction{
		TransactionID: "t2",
		Origin:        spec.ServerName(srv.ServerName()),
		Destination:   spec.ServerName("hs1"),
		PDUs: []json.RawMessage{
			message3.JSON(),
		},
	})
	waiter.Wait(t, 5*time.Second)
}

func TestInboundCanReturnMissingEvents(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		// Handle any transactions that the homeserver may send when connecting to another homeserver (such as presence)
		federation.HandleTransactionRequests(nil, nil),
	)
	cancel := srv.Listen()
	defer cancel()

	charlie := srv.UserID("charlie")
	roomVersion := alice.GetDefaultRoomVersion(t)

	for _, visibility := range []gomatrixserverlib.HistoryVisibility{
		gomatrixserverlib.HistoryVisibilityWorldReadable,
		gomatrixserverlib.HistoryVisibilityShared,
		gomatrixserverlib.HistoryVisibilityInvited,
		gomatrixserverlib.HistoryVisibilityJoined,
	} {
		// sytest: Inbound federation can return missing events for $vis visibility
		t.Run(fmt.Sprintf("Inbound federation can return missing events for %s visibility", visibility), func(t *testing.T) {
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset":  "public_chat",
				"version": roomVersion,
			})

			stateKey := ""
			// Set the history visibility for this room
			alice.SendEventSynced(t, roomID, b.Event{
				Type: spec.MRoomHistoryVisibility,
				Content: map[string]interface{}{
					"history_visibility": visibility,
				},
				StateKey: &stateKey,
			})

			alice.SendEventSynced(t, roomID, b.Event{
				Type: "m.room.message",
				Content: map[string]interface{}{
					"body":    "1",
					"msgtype": "m.text",
				},
				Sender: alice.UserID,
			})

			room := srv.MustJoinRoom(t, deployment, "hs1", roomID, charlie)
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(charlie, roomID))

			req := fclient.NewFederationRequest(
				"POST",
				spec.ServerName(srv.ServerName()),
				"hs1",
				fmt.Sprintf("/_matrix/federation/v1/get_missing_events/%s", roomID),
			)

			// Find two event IDs that there's going to be something missing
			// inbetween. Say, any history between the room's creation and my own
			// joining of it.
			earliestEvent := room.CurrentState("m.room.create", "")
			latestEvent := room.CurrentState("m.room.member", charlie)

			err := req.SetContent(map[string]interface{}{
				"earliest_events": []string{earliestEvent.EventID()},
				"latest_events":   []string{latestEvent.EventID()},
				"limit":           10,
				"min_depth":       1,
			})
			must.NotError(t, "failed to set content", err)

			result := fclient.RespMissingEvents{}
			err = srv.SendFederationRequest(context.Background(), t, deployment, req, &result)
			must.NotError(t, "get_missing_events failed", err)
			if len(result.Events) == 0 {
				t.Fatalf("no events returned from /get_missing_events")
			}

			// check that they are the *right* events. We expect copies of:
			// * the creator's join
			// * the power_levels
			// * the join rules
			// * the initial history_vis
			// * another history_vis, unless we tried to set it to the default (shared)
			// * the message
			events := result.Events.UntrustedEvents(roomVersion)

			verifyMsgEvent := func(ev gomatrixserverlib.PDU) {
				must.Equal(t, ev.Type(), "m.room.message", "not a message event")
				// if the history vis is 'joined' or 'invite', we should get redacted
				// copies of the events before we joined.
				if visibility == gomatrixserverlib.HistoryVisibilityJoined || visibility == gomatrixserverlib.HistoryVisibilityInvited {
					if !ev.Redacted() {
						t.Fatalf("Expected event to be redacted")
					}
				} else {
					for _, path := range []string{"msgtype", "body"} {
						if !gjson.Get(string(ev.Content()), path).Exists() {
							t.Fatalf("expected '%s' in content, but didn't find it: %s", path, string(ev.JSON()))
						}
					}
				}
			}

			for i, ev := range events {
				must.Equal(t, ev.RoomID().String(), roomID, "unexpected roomID")
				switch i {
				case 0:
					must.Equal(t, ev.Type(), "m.room.member", "not a membership event")
					must.Equal(t, *ev.StateKey(), alice.UserID, "unexpected creator")
				case 1:
					must.Equal(t, ev.Type(), spec.MRoomPowerLevels, "not a powerlevel event")
				case 2:
					must.Equal(t, ev.Type(), spec.MRoomJoinRules, "not a join_rules event")
				case 3:
					must.Equal(t, ev.Type(), spec.MRoomHistoryVisibility, "not a history_visiblity event")
				case 4:
					if visibility != gomatrixserverlib.HistoryVisibilityShared { // shared -> shared no-ops
						must.Equal(t, ev.Type(), spec.MRoomHistoryVisibility, "not a history_visiblity event")
					} else {
						verifyMsgEvent(ev)
					}
				case 5:
					// there shouldn't be any more events after the message event for shared history visibility
					if visibility == gomatrixserverlib.HistoryVisibilityShared {
						t.Fatalf("extra events returned: %+v", ev)
					}
					verifyMsgEvent(ev)
				case 6:
					t.Fatalf("extra events returned: %+v", ev)
				}
			}
		})
	}
}

// This test verifies that an event with a too large state_key can be used as a prev_event
// it is returned by a call to /get_missing_events and should pass event size checks.
// TODO: Do the same checks for type, user_id and sender
func TestOutboundFederationEventSizeGetMissingEvents(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		// Handle any transactions that the homeserver may send when connecting to another homeserver (such as presence)
		federation.HandleTransactionRequests(nil, nil),
	)
	cancel := srv.Listen()
	defer cancel()

	// register a handler for /get_missing_events, via a shim so that we can
	// behave differently as the test progresses.
	var onGetMissingEvents func(w http.ResponseWriter, req *http.Request)
	srv.Mux().HandleFunc("/_matrix/federation/v1/get_missing_events/{roomID}", func(w http.ResponseWriter, req *http.Request) {
		onGetMissingEvents(w, req)
	}).Methods("POST")

	ver := alice.GetDefaultRoomVersion(t)
	charlie := srv.UserID("charlie")
	room := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, charlie))
	roomAlias := srv.MakeAliasMapping("flibble", room.RoomID)
	// join the room
	alice.JoinRoom(t, roomAlias, nil)

	latestEvent := room.Timeline[len(room.Timeline)-1]

	// Sign this bad event which has a too large stateKey
	// Synapse always enforced 255 codepoints, but accepts events > 255 bytes.
	// Dendrite would fail to parse this event because it enforced 255 bytes, breaking older rooms.
	stateKey := strings.Repeat("💥", 70) // 280 bytes, 70 codepoints
	badEvent := b.Event{
		Type:     "my.room.breaker",
		StateKey: &stateKey,
		Sender:   charlie,
		Content:  map[string]interface{}{},
	}
	content, err := json.Marshal(badEvent.Content)
	if err != nil {
		t.Fatalf("failed to marshal badEvent content %+v", badEvent.Content)
	}
	roomVersion := gomatrixserverlib.MustGetRoomVersion(ver)
	pe := &gomatrixserverlib.ProtoEvent{
		SenderID:   badEvent.Sender,
		Depth:      int64(room.Depth + 1), // depth starts at 1
		Type:       badEvent.Type,
		StateKey:   badEvent.StateKey,
		Content:    content,
		RoomID:     room.RoomID,
		PrevEvents: room.ForwardExtremities,
	}
	eb := roomVersion.NewEventBuilderFromProtoEvent(pe)
	stateNeeded, err := gomatrixserverlib.StateNeededForProtoEvent(pe)
	if err != nil {
		t.Fatalf("failed to work out auth_events : %s", err)
	}
	eb.AuthEvents = room.AuthEvents(stateNeeded)

	signedBadEvent, err := eb.Build(time.Now(), spec.ServerName(srv.ServerName()), srv.KeyID, srv.Priv)
	switch e := err.(type) {
	case nil:
	case gomatrixserverlib.EventValidationError:
		// ignore for now
		t.Logf("EventValidationError: %v", e)
	default:
		t.Fatalf("failed to sign event: %s: %s", err, signedBadEvent.JSON())
	}
	room.AddEvent(signedBadEvent)

	// send the first "good" event, referencing the broken event as a prev_event
	sentEvent := srv.MustCreateEvent(t, room, federation.Event{
		Type:   "m.room.message",
		Sender: charlie,
		Content: map[string]interface{}{
			"body": "Message 2",
		},
	})
	room.AddEvent(sentEvent)

	waiter := helpers.NewWaiter()
	onGetMissingEvents = func(w http.ResponseWriter, req *http.Request) {
		defer waiter.Finish()
		must.MatchRequest(t, req, match.HTTPRequest{
			JSON: []match.JSON{
				match.JSONKeyEqual("earliest_events", []interface{}{latestEvent.EventID()}),
				match.JSONKeyEqual("latest_events", []interface{}{sentEvent.EventID()}),
			},
		})
		// return the bad event, which should result in the transaction failing.
		w.WriteHeader(200)
		res := struct {
			Events []json.RawMessage `json:"events"`
		}{
			Events: []json.RawMessage{signedBadEvent.JSON()},
		}
		var responseBytes []byte
		responseBytes, err = json.Marshal(&res)
		must.NotError(t, "failed to marshal response", err)
		w.Write(responseBytes)
	}

	fedClient := srv.FederationClient(deployment)
	resp, err := fedClient.SendTransaction(context.Background(), gomatrixserverlib.Transaction{
		TransactionID: "wut",
		Origin:        spec.ServerName(srv.ServerName()),
		Destination:   spec.ServerName("hs1"),
		PDUs: []json.RawMessage{
			sentEvent.JSON(),
		},
	})
	waiter.Wait(t, 5*time.Second)
	must.NotError(t, "SendTransaction errored", err)
	if len(resp.PDUs) != 1 {
		t.Fatalf("got %d errors, want 1", len(resp.PDUs))
	}
	_, ok := resp.PDUs[sentEvent.EventID()]
	if !ok {
		t.Fatalf("wrong PDU returned from send transaction, got %v want %s", resp.PDUs, sentEvent.EventID())
	}

	// Alice should receive the sent event, even though the "bad" event has a too large state key
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(room.RoomID, sentEvent.EventID()))
}
