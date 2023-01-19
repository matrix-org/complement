//go:build faster_joins
// +build faster_joins

// This file contains tests for joining rooms over federation, with the
// features introduced in msc2775.

package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/tidwall/gjson"

	"github.com/matrix-org/gomatrix"
	"github.com/matrix-org/gomatrixserverlib"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestPartialStateJoin(t *testing.T) {
	// createTestServer spins up a federation server suitable for the tests in this file
	createTestServer := func(t *testing.T, deployment *docker.Deployment, opts ...func(*federation.Server)) *federation.Server {
		t.Helper()

		return federation.NewServer(t, deployment,
			append(
				opts, // `opts` goes first so that it can override any of the following handlers
				federation.HandleKeyRequests(),
				federation.HandlePartialStateMakeSendJoinRequests(),
				federation.HandleEventRequests(),
				federation.HandleTransactionRequests(
					func(e *gomatrixserverlib.Event) {
						t.Fatalf("Received unexpected PDU: %s", string(e.JSON()))
					},
					// the homeserver under test may send us presence when the joining user syncs
					nil,
				),
			)...,
		)
	}

	// createMemberEvent creates a membership event for the given user
	createMembershipEvent := func(
		t *testing.T, signingServer *federation.Server, room *federation.ServerRoom, userId string,
		membership string,
	) *gomatrixserverlib.Event {
		t.Helper()

		return signingServer.MustCreateEvent(t, room, b.Event{
			Type:     "m.room.member",
			StateKey: b.Ptr(userId),
			Sender:   userId,
			Content: map[string]interface{}{
				"membership": membership,
			},
		})
	}

	// createJoinEvent creates a join event for the given user
	createJoinEvent := func(
		t *testing.T, signingServer *federation.Server, room *federation.ServerRoom, userId string,
	) *gomatrixserverlib.Event {
		t.Helper()

		return createMembershipEvent(t, signingServer, room, userId, "join")
	}

	// createLeaveEvent creates a leave event for the given user
	createLeaveEvent := func(
		t *testing.T, signingServer *federation.Server, room *federation.ServerRoom, userId string,
	) *gomatrixserverlib.Event {
		t.Helper()

		return createMembershipEvent(t, signingServer, room, userId, "leave")
	}

	// createTestRoom creates a room on the complement server suitable for many of the tests in this file
	// The room starts with @charlie and @derek in it
	createTestRoom := func(t *testing.T, server *federation.Server, roomVer gomatrixserverlib.RoomVersion) *federation.ServerRoom {
		t.Helper()

		// create the room on the complement server, with charlie and derek as members
		serverRoom := server.MustMakeRoom(t, roomVer, federation.InitialRoomEvents(roomVer, server.UserID("charlie")))
		serverRoom.AddEvent(createJoinEvent(t, server, serverRoom, server.UserID("derek")))
		return serverRoom
	}

	// getSyncToken gets the latest sync token
	getSyncToken := func(t *testing.T, alice *client.CSAPI) string {
		t.Helper()

		_, syncToken := alice.MustSync(t,
			client.SyncReq{
				Filter:        buildLazyLoadingSyncFilter(nil),
				TimeoutMillis: "0",
			},
		)
		return syncToken
	}

	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	// test that a regular /sync request made during a partial-state /send_join
	// request blocks until the state is correctly synced.
	t.Run("SyncBlocksDuringPartialStateJoin", func(t *testing.T) {
		alice := deployment.RegisterUser(t, "hs1", "t1alice", "secret", false)

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		// Alice has now joined the room, and the server is syncing the state in the background.

		// attempts to sync should now block. Fire off a goroutine to try it.
		syncResponseChan := make(chan gjson.Result)
		go func() {
			response, _ := alice.MustSync(t, client.SyncReq{})
			syncResponseChan <- response
			close(syncResponseChan)
		}()

		// wait for the state_ids request to arrive
		psjResult.AwaitStateIdsRequest(t)

		// the client-side requests should still be waiting
		select {
		case <-syncResponseChan:
			t.Fatalf("Sync completed before state resync complete")
		default:
		}

		// release the federation /state response
		psjResult.FinishStateRequest()

		// the /sync request should now complete, with the new room
		var syncRes gjson.Result
		select {
		case <-time.After(1 * time.Second):
			t.Fatalf("/sync request request did not complete")
		case syncRes = <-syncResponseChan:
		}

		roomRes := syncRes.Get("rooms.join." + client.GjsonEscape(serverRoom.RoomID))
		if !roomRes.Exists() {
			t.Fatalf("/sync completed without join to new room\n")
		}

		// check that the state includes both charlie and derek.
		matcher := match.JSONCheckOffAllowUnwanted("state.events",
			[]interface{}{
				"m.room.member|" + server.UserID("charlie"),
				"m.room.member|" + server.UserID("derek"),
			}, func(result gjson.Result) interface{} {
				return strings.Join([]string{result.Map()["type"].Str, result.Map()["state_key"].Str}, "|")
			}, nil,
		)
		if err := matcher([]byte(roomRes.Raw)); err != nil {
			t.Errorf("Did not find expected state events in /sync response: %s", err)

		}
	})

	// when Alice does a lazy-loading sync, she should see the room immediately
	t.Run("CanLazyLoadingSyncDuringPartialStateJoin", func(t *testing.T) {
		alice := deployment.RegisterUser(t, "hs1", "t2alice", "secret", false)

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		alice.MustSyncUntil(t,
			client.SyncReq{
				Filter: buildLazyLoadingSyncFilter(nil),
			},
			client.SyncJoinedTo(alice.UserID, serverRoom.RoomID),
		)
		t.Logf("Alice successfully synced")
	})

	// we should be able to send events in the room, during the resync
	t.Run("CanSendEventsDuringPartialStateJoin", func(t *testing.T) {
		alice := deployment.RegisterUser(t, "hs1", "t3alice", "secret", false)

		pdusChannel := make(chan *gomatrixserverlib.Event)
		server := createTestServer(
			t,
			deployment,
			federation.HandleTransactionRequests(
				func(e *gomatrixserverlib.Event) {
					pdusChannel <- e
				},
				// we don't expect EDUs
				func(e gomatrixserverlib.EDU) {
					t.Fatalf("Received unexpected EDU: %s", e.Content)
				},
			),
		)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		alice.Client.Timeout = 2 * time.Second
		paths := []string{"_matrix", "client", "v3", "rooms", serverRoom.RoomID, "send", "m.room.message", "0"}
		res := alice.MustDoFunc(t, "PUT", paths, client.WithJSONBody(t, map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world!",
		}))
		body := gjson.ParseBytes(client.ParseJSON(t, res))
		eventID := body.Get("event_id").Str
		t.Logf("Alice sent event event ID %s", eventID)

		select {
		case pdu := <-pdusChannel:
			if !(pdu.Type() == "m.room.message") {
				t.Error("Received PDU is not of type m.room.message")
			}
		case <-time.After(1 * time.Second):
			t.Error("Message PDU not received after one second")
		}
	})

	// we should be able to receive typing EDU over federation during the resync
	t.Run("CanReceiveTypingDuringPartialStateJoin", func(t *testing.T) {
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		// Derek starts typing in the room.
		derekUserId := psjResult.Server.UserID("derek")
		content, _ := json.Marshal(map[string]interface{}{
			"room_id": serverRoom.RoomID,
			"user_id": derekUserId,
			"typing":  true,
		})
		edu := gomatrixserverlib.EDU{
			Type:    "m.typing",
			Content: content,
		}
		psjResult.Server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{}, []gomatrixserverlib.EDU{edu})

		// Alice should be able to see that Derek is typing (even though HS1 is resyncing).
		aliceNextBatch := alice.MustSyncUntil(t,
			client.SyncReq{
				Filter: buildLazyLoadingSyncFilter(nil),
			},
			client.SyncEphemeralHas(serverRoom.RoomID, func(result gjson.Result) bool {
				if result.Get("type").Str != "m.typing" {
					return false
				}
				user_ids := result.Get("content.user_ids").Array()
				if len(user_ids) != 1 {
					return false
				}
				return user_ids[0].Str == derekUserId
			}),
		)

		// Alice should still be able to see incoming PDUs in the room during
		// the resync; the earlier EDU shouldn't interfere with this.
		// (See https://github.com/matrix-org/synapse/issues/13684)
		event := psjResult.CreateMessageEvent(t, "charlie", nil)
		serverRoom.AddEvent(event)
		server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{event.JSON()}, nil)
		aliceNextBatch = awaitEventViaSync(t, alice, serverRoom.RoomID, event.EventID(), aliceNextBatch)

		// The resync completes.
		psjResult.FinishStateRequest()

		// Derek stops typing.
		content, _ = json.Marshal(map[string]interface{}{
			"room_id": serverRoom.RoomID,
			"user_id": derekUserId,
			"typing":  false,
		})
		edu = gomatrixserverlib.EDU{
			Type:    "m.typing",
			Content: content,
		}
		psjResult.Server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{}, []gomatrixserverlib.EDU{edu})

		// Alice should be able to see that no-one is typing.
		alice.MustSyncUntil(t,
			client.SyncReq{
				Filter: buildLazyLoadingSyncFilter(nil),
				Since:  aliceNextBatch,
			},
			client.SyncEphemeralHas(serverRoom.RoomID, func(result gjson.Result) bool {
				return (result.Get("type").Str == "m.typing" &&
					result.Get("content.user_ids.#").Int() == 0)
			}),
		)

	})

	// we should be able to receive presence EDU over federation during the resync
	t.Run("CanReceivePresenceDuringPartialStateJoin", func(t *testing.T) {
		// See https://github.com/matrix-org/synapse/issues/13008")
		t.Skip("Presence EDUs are currently dropped during a resync")
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		derekUserId := psjResult.Server.UserID("derek")

		content, _ := json.Marshal(map[string]interface{}{
			"push": []map[string]interface{}{
				map[string]interface{}{
					"user_id":         derekUserId,
					"presence":        "online",
					"last_active_ago": 100,
				},
			},
		})
		edu := gomatrixserverlib.EDU{
			Type:    "m.presence",
			Content: content,
		}
		psjResult.Server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{}, []gomatrixserverlib.EDU{edu})

		alice.MustSyncUntil(t,
			client.SyncReq{
				Filter: buildLazyLoadingSyncFilter(nil),
			},
			func(userID string, sync gjson.Result) error {
				for _, e := range sync.Get("presence").Get("events").Array() {
					if e.Get("sender").Str == derekUserId {
						return nil
					}
				}
				return fmt.Errorf("No presence update from %s", derekUserId)
			},
		)

		psjResult.FinishStateRequest()
	})

	// we should be able to receive to_device EDU over federation during the resync
	t.Run("CanReceiveToDeviceDuringPartialStateJoin", func(t *testing.T) {
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		// Send a to-device message from Derek to Alice.
		derekUserId := psjResult.Server.UserID("derek")
		messageId := "hiezohf6Hoo7kaev"
		content, _ := json.Marshal(map[string]interface{}{
			"message_id": messageId,
			"sender":     derekUserId,
			"type":       "m.test",
			"messages": map[string]interface{}{
				alice.UserID: map[string]interface{}{
					"*": map[string]interface{}{},
				},
			},
		})
		edu := gomatrixserverlib.EDU{
			Type:    "m.direct_to_device",
			Content: content,
		}
		psjResult.Server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{}, []gomatrixserverlib.EDU{edu})

		// Alice should see Derek's to-device message when she syncs.
		alice.MustSyncUntil(t,
			client.SyncReq{
				Filter: buildLazyLoadingSyncFilter(nil),
			},
			func(userID string, sync gjson.Result) error {
				for _, e := range sync.Get("to_device.events").Array() {
					if e.Get("sender").Str == derekUserId &&
						e.Get("type").Str == "m.test" {
						return nil
					}
				}
				return fmt.Errorf("No to_device update from %s", derekUserId)
			},
		)
		psjResult.FinishStateRequest()
	})

	// we should be able to receive receipt EDU over federation during the resync
	t.Run("CanReceiveReceiptDuringPartialStateJoin", func(t *testing.T) {
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		derekUserId := psjResult.Server.UserID("derek")

		// Derek sends a read receipt into the room.
		content, _ := json.Marshal(map[string]interface{}{
			serverRoom.RoomID: map[string]interface{}{
				"m.read": map[string]interface{}{
					derekUserId: map[string]interface{}{
						"data": map[string]interface{}{
							"ts": 1436451550453,
						},
						"event_ids": []string{"mytesteventid"},
					},
				},
			},
		})
		edu := gomatrixserverlib.EDU{
			Type:    "m.receipt",
			Content: content,
		}
		psjResult.Server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{}, []gomatrixserverlib.EDU{edu})

		// Alice should be able to see Derek's read receipt during the resync
		alice.MustSyncUntil(t,
			client.SyncReq{
				Filter: buildLazyLoadingSyncFilter(nil),
			},
			client.SyncEphemeralHas(serverRoom.RoomID, func(result gjson.Result) bool {
				if result.Get("type").Str != "m.receipt" {
					return false
				}

				if result.Get("content").Get("mytesteventid").Get("m\\.read").Get(strings.Replace(derekUserId, ".", "\\.", -1)).Get("ts").Int() == 1436451550453 {
					return true
				}
				return false
			}),
		)
		psjResult.FinishStateRequest()
	})

	// we should be able to receive device list update EDU over federation during the resync
	t.Run("CanReceiveDeviceListUpdateDuringPartialStateJoin", func(t *testing.T) {
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		derekUserId := psjResult.Server.UserID("derek")

		content, _ := json.Marshal(map[string]interface{}{
			"device_id": "QBUAZIFURK",
			"stream_id": 1,
			"user_id":   derekUserId,
		})
		edu := gomatrixserverlib.EDU{
			Type:    "m.device_list_update",
			Content: content,
		}
		aliceNextBatch := getSyncToken(t, alice)
		psjResult.Server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{}, []gomatrixserverlib.EDU{edu})

		// The resync completes.
		psjResult.FinishStateRequest()

		// Check that Alice is told that Derek's devices have changed.
		// (Alice does not get told this during the resync, since we can't know
		// for certain who is in that room until the resync completes.)
		aliceNextBatch = alice.MustSyncUntil(
			t,
			client.SyncReq{
				Filter: buildLazyLoadingSyncFilter(nil),
				Since:  aliceNextBatch,
			},
			func(clientUserID string, res gjson.Result) error {
				matcher := match.JSONCheckOff(
					"device_lists.changed",
					[]interface{}{derekUserId},
					func(r gjson.Result) interface{} { return r.Str },
					nil,
				)
				return matcher([]byte(res.Raw))
			},
		)
	})

	// we should be able to receive signing key update EDU over federation during the resync
	t.Run("CanReceiveSigningKeyUpdateDuringPartialStateJoin", func(t *testing.T) {
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		derekUserId := psjResult.Server.UserID("derek")

		content, _ := json.Marshal(map[string]interface{}{
			"user_id": derekUserId,
		})
		edu := gomatrixserverlib.EDU{
			Type:    "m.signing_key_update",
			Content: content,
		}
		psjResult.Server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{}, []gomatrixserverlib.EDU{edu})

		// If we want to check the sync we need to have an encrypted room,
		// for now just check that the fed transaction is accepted.
	})

	// we should be able to receive events over federation during the resync
	t.Run("CanReceiveEventsDuringPartialStateJoin", func(t *testing.T) {
		alice := deployment.RegisterUser(t, "hs1", "t4alice", "secret", false)
		syncToken := getSyncToken(t, alice)

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		// the HS will make an /event_auth request for the event
		federation.HandleEventAuthRequests()(server)

		event := psjResult.CreateMessageEvent(t, "derek", nil)
		t.Logf("Derek created event with ID %s", event.EventID())

		// derek sends an event in the room
		testReceiveEventDuringPartialStateJoin(t, deployment, alice, psjResult, event, syncToken)
	})

	// we should be able to receive events with a missing prev event over federation during the resync
	t.Run("CanReceiveEventsWithMissingParentsDuringPartialStateJoin", func(t *testing.T) {
		alice := deployment.RegisterUser(t, "hs1", "t5alice", "secret", false)
		syncToken := getSyncToken(t, alice)

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		// we construct the following event graph:
		// ... <-- M <-- A <-- B
		//
		// M is @t5alice:hs1's join event.
		// A and B are regular m.room.messsage events created by @derek on the Complement homeserver.
		//
		// initially, hs1 only knows about event M.
		// we send only event B to hs1.
		eventM := serverRoom.CurrentState("m.room.member", alice.UserID)
		eventA := psjResult.CreateMessageEvent(t, "derek", []string{eventM.EventID()})
		eventB := psjResult.CreateMessageEvent(t, "derek", []string{eventA.EventID()})
		t.Logf("%s's m.room.member event is %s", *eventM.StateKey(), eventM.EventID())
		t.Logf("Derek created event A with ID %s", eventA.EventID())
		t.Logf("Derek created event B with ID %s", eventB.EventID())

		// the HS will make an /event_auth request for event A
		federation.HandleEventAuthRequests()(server)

		// the HS will make a /get_missing_events request for the missing prev events of event B
		handleGetMissingEventsRequests(t, server, serverRoom,
			[]string{eventB.EventID()}, []*gomatrixserverlib.Event{eventA})

		// send event B to hs1
		testReceiveEventDuringPartialStateJoin(t, deployment, alice, psjResult, eventB, syncToken)
	})

	// we should be able to receive events with partially missing prev events over federation during the resync
	t.Run("CanReceiveEventsWithHalfMissingParentsDuringPartialStateJoin", func(t *testing.T) {
		alice := deployment.RegisterUser(t, "hs1", "t6alice", "secret", false)
		syncToken := getSyncToken(t, alice)

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		// we construct the following event graph:
		//         +---------+
		//         v          \
		// ... <-- M <-- A <-- B
		//
		// M is @t6alice:hs1's join event.
		// A and B are regular m.room.messsage events created by @derek on the Complement homeserver.
		//
		// initially, hs1 only knows about event M.
		// we send only event B to hs1.
		eventM := serverRoom.CurrentState("m.room.member", alice.UserID)
		eventA := psjResult.CreateMessageEvent(t, "derek", []string{eventM.EventID()})
		eventB := psjResult.CreateMessageEvent(t, "derek", []string{eventA.EventID(), eventM.EventID()})
		t.Logf("%s's m.room.member event is %s", *eventM.StateKey(), eventM.EventID())
		t.Logf("Derek created event A with ID %s", eventA.EventID())
		t.Logf("Derek created event B with ID %s", eventB.EventID())

		// the HS will make an /event_auth request for event A
		federation.HandleEventAuthRequests()(server)

		// the HS will make a /get_missing_events request for the missing prev event of event B
		handleGetMissingEventsRequests(t, server, serverRoom,
			[]string{eventB.EventID()}, []*gomatrixserverlib.Event{eventA})

		// send event B to hs1
		testReceiveEventDuringPartialStateJoin(t, deployment, alice, psjResult, eventB, syncToken)
	})

	// we should be able to receive events with a missing prev event, with half missing prev events,
	// over federation during the resync
	t.Run("CanReceiveEventsWithHalfMissingGrandparentsDuringPartialStateJoin", func(t *testing.T) {
		alice := deployment.RegisterUser(t, "hs1", "t7alice", "secret", false)
		syncToken := getSyncToken(t, alice)

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		// we construct the following event graph:
		//         +---------+
		//         v          \
		// ... <-- M <-- A <-- B <-- C
		//
		// M is @t7alice:hs1's join event.
		// A, B and C are regular m.room.messsage events created by @derek on the Complement homeserver.
		//
		// initially, hs1 only knows about event M.
		// we send only event C to hs1.
		eventM := serverRoom.CurrentState("m.room.member", alice.UserID)
		eventA := psjResult.CreateMessageEvent(t, "derek", []string{eventM.EventID()})
		eventB := psjResult.CreateMessageEvent(t, "derek", []string{eventA.EventID(), eventM.EventID()})
		eventC := psjResult.CreateMessageEvent(t, "derek", []string{eventB.EventID()})
		t.Logf("%s's m.room.member event is %s", *eventM.StateKey(), eventM.EventID())
		t.Logf("Derek created event A with ID %s", eventA.EventID())
		t.Logf("Derek created event B with ID %s", eventB.EventID())
		t.Logf("Derek created event C with ID %s", eventC.EventID())

		// the HS will make a /get_missing_events request for the missing prev event of event C,
		// to which we respond with event B only.
		handleGetMissingEventsRequests(t, server, serverRoom,
			[]string{eventC.EventID()}, []*gomatrixserverlib.Event{eventB})

		// dedicated state_ids and state handlers for event A
		handleStateIdsRequests(t, server, serverRoom, eventA.EventID(), serverRoom.AllCurrentState(), nil, nil)
		handleStateRequests(t, server, serverRoom, eventA.EventID(), serverRoom.AllCurrentState(), nil, nil)

		// send event C to hs1
		testReceiveEventDuringPartialStateJoin(t, deployment, alice, psjResult, eventC, syncToken)
	})

	// initial sync must return memberships of event senders even when they aren't present in the
	// partial room state.
	t.Run("Lazy-loading initial sync includes remote memberships during partial state join", func(t *testing.T) {
		alice := deployment.RegisterUser(t, "hs1", "t8alice", "secret", false)

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		// the HS will make an /event_auth request for the event
		federation.HandleEventAuthRequests()(server)

		// derek sends a message into the room.
		event := psjResult.CreateMessageEvent(t, "derek", nil)
		t.Logf("Derek created event with ID %s", event.EventID())
		psjResult.Server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{event.JSON()}, nil)

		// wait for the homeserver to persist the event.
		awaitEventArrival(t, time.Second, alice, serverRoom.RoomID, event.EventID())

		// do a lazy-loading initial sync.
		syncRes, _ := alice.MustSync(t,
			client.SyncReq{
				Since:  "",
				Filter: buildLazyLoadingSyncFilter(nil),
			},
		)

		err := client.SyncStateHas(serverRoom.RoomID, func(ev gjson.Result) bool {
			return ev.Get("type").Str == "m.room.member" && ev.Get("state_key").Str == event.Sender()
		})(alice.UserID, syncRes)
		if err != nil {
			t.Errorf("Did not find %s's m.room.member event in lazy-loading /sync response: %s", event.Sender(), err)
		}
	})

	// gappy sync must return memberships of event senders even when they aren't present in the
	// partial room state.
	t.Run("Lazy-loading gappy sync includes remote memberships during partial state join", func(t *testing.T) {
		alice := deployment.RegisterUser(t, "hs1", "t9alice", "secret", false)
		syncToken := getSyncToken(t, alice)

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		syncToken = alice.MustSyncUntil(t,
			client.SyncReq{
				Since:  syncToken,
				Filter: buildLazyLoadingSyncFilter(nil),
			},
			client.SyncJoinedTo(alice.UserID, serverRoom.RoomID),
		)

		// the HS will make an /event_auth request for the event
		federation.HandleEventAuthRequests()(server)

		// derek sends two messages into the room.
		event1 := psjResult.CreateMessageEvent(t, "derek", nil)
		event2 := psjResult.CreateMessageEvent(t, "derek", nil)
		t.Logf("Derek created event 1 with ID %s", event1.EventID())
		t.Logf("Derek created event 2 with ID %s", event2.EventID())
		psjResult.Server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{event1.JSON(), event2.JSON()}, nil)

		// wait for the homeserver to persist the event.
		awaitEventArrival(t, time.Second, alice, serverRoom.RoomID, event2.EventID())

		// do a gappy sync which only picks up the second message.
		syncRes, _ := alice.MustSync(t,
			client.SyncReq{
				Since: syncToken,
				Filter: buildLazyLoadingSyncFilter(map[string]interface{}{
					"limit": 1,
				}),
			},
		)

		if !syncRes.Get("rooms.join." + client.GjsonEscape(serverRoom.RoomID) + ".timeline.limited").Bool() {
			t.Errorf("/sync response was not gappy")
		}

		err := client.SyncTimelineHas(serverRoom.RoomID, func(ev gjson.Result) bool {
			return ev.Get("event_id").Str == event1.EventID()
		})(alice.UserID, syncRes)
		if err == nil {
			t.Errorf("gappy /sync returned the first event unexpectedly")
		}

		err = client.SyncTimelineHas(serverRoom.RoomID, func(ev gjson.Result) bool {
			return ev.Get("event_id").Str == event2.EventID()
		})(alice.UserID, syncRes)
		if err != nil {
			t.Errorf("Did not find event 2 in lazy-loading /sync response: %s", err)
		}

		err = client.SyncStateHas(serverRoom.RoomID, func(ev gjson.Result) bool {
			return ev.Get("type").Str == "m.room.member" && ev.Get("state_key").Str == event2.Sender()
		})(alice.UserID, syncRes)
		if err != nil {
			t.Errorf("Did not find %s's m.room.member event in lazy-loading /sync response: %s", event2.Sender(), err)
		}
	})

	// incremental sync must return memberships of event senders even when they aren't present in
	// the partial room state.
	t.Run("Lazy-loading incremental sync includes remote memberships during partial state join", func(t *testing.T) {
		alice := deployment.RegisterUser(t, "hs1", "t10alice", "secret", false)
		syncToken := getSyncToken(t, alice)

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		syncToken = alice.MustSyncUntil(t,
			client.SyncReq{
				Since:  syncToken,
				Filter: buildLazyLoadingSyncFilter(nil),
			},
			client.SyncJoinedTo(alice.UserID, serverRoom.RoomID),
		)

		// the HS will make an /event_auth request for the event
		federation.HandleEventAuthRequests()(server)

		// derek sends a message into the room.
		event := psjResult.CreateMessageEvent(t, "derek", nil)
		psjResult.Server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{event.JSON()}, nil)
		t.Logf("Derek created event with ID %s", event.EventID())

		// wait for the homeserver to persist the event.
		awaitEventArrival(t, time.Second, alice, serverRoom.RoomID, event.EventID())

		// do an incremental sync.
		syncRes, _ := alice.MustSync(t,
			client.SyncReq{
				Since:  syncToken,
				Filter: buildLazyLoadingSyncFilter(nil),
			},
		)

		err := client.SyncStateHas(serverRoom.RoomID, func(ev gjson.Result) bool {
			return ev.Get("type").Str == "m.room.member" && ev.Get("state_key").Str == event.Sender()
		})(alice.UserID, syncRes)
		if err != nil {
			t.Errorf("Did not find %s's m.room.member event in lazy-loading /sync response: %s", event.Sender(), err)
		}
	})

	// a request to (client-side) /members?at= should block until the (federation) /state request completes
	// TODO(faster_joins): also need to test /state, and /members without an `at`, which follow a different path
	t.Run("MembersRequestBlocksDuringPartialStateJoin", func(t *testing.T) {
		alice := deployment.RegisterUser(t, "hs1", "t11alice", "secret", false)

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		// we need a sync token to pass to the `at` param.
		syncToken := alice.MustSyncUntil(t,
			client.SyncReq{
				Filter: buildLazyLoadingSyncFilter(nil),
			},
			client.SyncJoinedTo(alice.UserID, serverRoom.RoomID),
		)
		t.Logf("Alice successfully synced")

		// Fire off a goroutine to send the request, and write the response back to a channel.
		clientMembersRequestResponseChan := make(chan *http.Response)
		go func() {
			queryParams := url.Values{}
			queryParams.Set("at", syncToken)
			clientMembersRequestResponseChan <- alice.MustDoFunc(
				t,
				"GET",
				[]string{"_matrix", "client", "v3", "rooms", serverRoom.RoomID, "members"},
				client.WithQueries(queryParams),
			)
			close(clientMembersRequestResponseChan)
		}()

		// release the federation /state response
		psjResult.FinishStateRequest()

		// the client-side /members request should now complete, with a response that includes charlie and derek.
		select {
		case <-time.After(1 * time.Second):
			t.Fatalf("client-side /members request did not complete")
		case res := <-clientMembersRequestResponseChan:
			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONCheckOff("chunk",
						[]interface{}{
							"m.room.member|" + alice.UserID,
							"m.room.member|" + server.UserID("charlie"),
							"m.room.member|" + server.UserID("derek"),
						}, func(result gjson.Result) interface{} {
							return strings.Join([]string{result.Map()["type"].Str, result.Map()["state_key"].Str}, "|")
						}, nil),
				},
			})
		}
	})

	// test that a partial-state join continues syncing state after a restart
	// the same as SyncBlocksDuringPartialStateJoin, with a restart in the middle
	t.Run("PartialStateJoinContinuesAfterRestart", func(t *testing.T) {
		alice := deployment.RegisterUser(t, "hs1", "t12alice", "secret", false)

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		// Alice has now joined the room, and the server is syncing the state in the background.

		// wait for the state_ids request to arrive
		psjResult.AwaitStateIdsRequest(t)

		// restart the homeserver
		err := deployment.Restart(t)
		if err != nil {
			t.Errorf("Failed to restart homeserver: %s", err)
		}

		// attempts to sync should block. Fire off a goroutine to try it.
		syncResponseChan := make(chan gjson.Result)
		go func() {
			response, _ := alice.MustSync(t, client.SyncReq{})
			syncResponseChan <- response
			close(syncResponseChan)
		}()

		// we expect another state_ids request to arrive.
		// we'd do another AwaitStateIdsRequest, except it's single-use.

		// the client-side requests should still be waiting
		select {
		case <-syncResponseChan:
			t.Fatalf("Sync completed before state resync complete")
		default:
		}

		// release the federation /state response
		psjResult.FinishStateRequest()

		// the /sync request should now complete, with the new room
		var syncRes gjson.Result
		select {
		case <-time.After(1 * time.Second):
			t.Fatalf("/sync request request did not complete")
		case syncRes = <-syncResponseChan:
		}

		roomRes := syncRes.Get("rooms.join." + client.GjsonEscape(serverRoom.RoomID))
		if !roomRes.Exists() {
			t.Fatalf("/sync completed without join to new room\n")
		}
	})

	// test that a partial-state join can fall back to other homeservers when re-syncing
	// partial state.
	t.Run("PartialStateJoinSyncsUsingOtherHomeservers", func(t *testing.T) {
		// set up 3 homeservers: hs1, hs2 and complement
		deployment := Deploy(t, b.BlueprintFederationTwoLocalOneRemote)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")
		charlie := deployment.Client(t, "hs2", "@charlie:hs2")

		// create a public room
		roomID := alice.CreateRoom(t, map[string]interface{}{
			"preset": "public_chat",
		})

		// create the complement homeserver
		server := createTestServer(t, deployment)
		cancelListener := server.Listen()
		defer cancelListener()

		// join complement to the public room
		room := server.MustJoinRoom(t, deployment, "hs1", roomID, server.UserID("david"))

		// we expect a /state_ids request from hs2 after it joins the room
		// we will respond to the request with garbage
		fedStateIdsRequestReceivedWaiter := NewWaiter()
		fedStateIdsSendResponseWaiter := NewWaiter()
		server.Mux().Handle(
			fmt.Sprintf("/_matrix/federation/v1/state_ids/%s", roomID),
			http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				queryParams := req.URL.Query()
				t.Logf("Incoming state_ids request for event %s in room %s", queryParams["event_id"], roomID)
				fedStateIdsRequestReceivedWaiter.Finish()
				fedStateIdsSendResponseWaiter.Wait(t, 60*time.Second)
				t.Logf("Replying to /state_ids request with invalid response")

				w.WriteHeader(200)

				if _, err := w.Write([]byte("{}")); err != nil {
					t.Errorf("Error writing to request: %v", err)
				}
			}),
		).Methods("GET")

		// join charlie on hs2 to the room, via the complement homeserver
		charlie.JoinRoom(t, roomID, []string{server.ServerName()})

		// and let hs1 know that charlie has joined,
		// otherwise hs1 will refuse /state_ids requests
		member_event := room.CurrentState("m.room.member", charlie.UserID).JSON()
		server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{member_event}, nil)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(charlie.UserID, roomID))

		// wait until hs2 starts syncing state
		fedStateIdsRequestReceivedWaiter.Waitf(t, 5*time.Second, "Waiting for /state_ids request")

		syncResponseChan := make(chan gjson.Result)
		go func() {
			response, _ := charlie.MustSync(t, client.SyncReq{})
			syncResponseChan <- response
			close(syncResponseChan)
		}()

		// the client-side requests should still be waiting
		select {
		case <-syncResponseChan:
			t.Fatalf("hs2 sync completed before state resync complete")
		default:
		}

		// reply to hs2 with a bogus /state_ids response
		fedStateIdsSendResponseWaiter.Finish()

		// charlie's /sync request should now complete, with the new room
		var syncRes gjson.Result
		select {
		case <-time.After(1 * time.Second):
			t.Fatalf("hs2 /sync request request did not complete")
		case syncRes = <-syncResponseChan:
		}

		roomRes := syncRes.Get("rooms.join." + client.GjsonEscape(roomID))
		if !roomRes.Exists() {
			t.Fatalf("hs2 /sync completed without join to new room\n")
		}
	})

	// test a lazy-load-members sync while re-syncing partial state, followed by completion of state syncing,
	// followed by a gappy sync. the gappy sync should include the correct member state,
	// since it was not sent on the previous sync.
	t.Run("GappySyncAfterPartialStateSynced", func(t *testing.T) {
		alice := deployment.RegisterUser(t, "hs1", "t13alice", "secret", false)

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		// get a sync token before state syncing finishes.
		syncToken := alice.MustSyncUntil(t,
			client.SyncReq{
				Filter: buildLazyLoadingSyncFilter(nil),
			},
			client.SyncJoinedTo(alice.UserID, serverRoom.RoomID),
		)
		t.Logf("Alice successfully synced")

		// wait for partial state to finish syncing,
		// by waiting for the room to show up in a regular /sync.
		psjResult.AwaitStateIdsRequest(t)
		psjResult.FinishStateRequest()
		alice.MustSyncUntil(t,
			client.SyncReq{},
			client.SyncJoinedTo(alice.UserID, serverRoom.RoomID),
		)

		// make derek send two messages into the room.
		// we will do a gappy sync after, which will only pick up the last message.
		var lastEventID string
		for i := 0; i < 2; i++ {
			event := server.MustCreateEvent(t, serverRoom, b.Event{
				Type:   "m.room.message",
				Sender: server.UserID("derek"),
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    "Message " + strconv.Itoa(i),
				},
			})
			lastEventID = event.EventID()
			serverRoom.AddEvent(event)
			server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{event.JSON()}, nil)
		}

		// wait for the events to come down a regular /sync.
		alice.MustSyncUntil(t,
			client.SyncReq{},
			client.SyncTimelineHasEventID(serverRoom.RoomID, lastEventID),
		)

		// now do a gappy sync using the sync token from before.
		syncRes, _ := alice.MustSync(t,
			client.SyncReq{
				Since: syncToken,
				Filter: buildLazyLoadingSyncFilter(map[string]interface{}{
					"limit": 1,
				}),
			},
		)

		// check that the state includes derek.
		roomRes := syncRes.Get("rooms.join." + client.GjsonEscape(serverRoom.RoomID))
		if !roomRes.Exists() {
			t.Fatalf("/sync completed without join to new room\n")
		}
		t.Logf("gappy /sync response for %s: %s", serverRoom.RoomID, roomRes)

		timelineMatcher := match.JSONCheckOff("timeline.events",
			[]interface{}{lastEventID},
			func(result gjson.Result) interface{} {
				return result.Map()["event_id"].Str
			}, nil,
		)
		stateMatcher := match.JSONCheckOffAllowUnwanted("state.events",
			[]interface{}{
				"m.room.member|" + server.UserID("derek"),
			}, func(result gjson.Result) interface{} {
				return strings.Join([]string{result.Map()["type"].Str, result.Map()["state_key"].Str}, "|")
			}, nil,
		)
		if err := timelineMatcher([]byte(roomRes.Raw)); err != nil {
			t.Errorf("Unexpected timeline events found in gappy /sync response: %s", err)
		}
		if err := stateMatcher([]byte(roomRes.Raw)); err != nil {
			t.Errorf("Did not find derek's m.room.member event in gappy /sync response: %s", err)
		}
	})

	// regression test for https://github.com/matrix-org/synapse/issues/13001
	//
	// There was an edge case where, if we initially receive lots of events as outliers,
	// and they then get de-outliered as partial state events, we would get stuck in
	// an infinite loop of de-partial-stating.
	t.Run("Resync completes even when events arrive before their prev_events", func(t *testing.T) {
		alice := deployment.RegisterUser(t, "hs1", "t14alice", "secret", false)
		syncToken := getSyncToken(t, alice)

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		// Alice has now joined the room, and the server is syncing the state in the background.

		// utility function to wait for a given event to arrive at the remote server.
		// This works simply by polling /event until we get a 200.

		// here's the first event which we *ought* to un-partial-state, but won't
		lateEvent := psjResult.CreateMessageEvent(t, "charlie", nil)

		// next, we want to create 100 outliers. So, charlie creates 100 state events, and
		// then persuades the system under test to create a backwards extremity using those events as
		// part of the room state.
		outliers := make([]*gomatrixserverlib.Event, 100)
		outlierEventIDs := make([]string, len(outliers))
		for i := range outliers {
			body := fmt.Sprintf("outlier event %d", i)
			outliers[i] = server.MustCreateEvent(t, serverRoom, b.Event{
				Type:     "outlier_state",
				Sender:   server.UserID("charlie"),
				StateKey: b.Ptr(fmt.Sprintf("state_%d", i)),
				Content:  map[string]interface{}{"body": body},
			})
			serverRoom.AddEvent(outliers[i])
			outlierEventIDs[i] = outliers[i].EventID()
		}
		t.Logf("Created outliers: %s ... %s", outliers[0].EventID(), outliers[len(outliers)-1].EventID())

		// a couple of regular timeline events to pull in the outliers... Note that these are persisted with *full*
		// state rather than becoming partial state events.
		timelineEvent1 := psjResult.CreateMessageEvent(t, "charlie", nil)
		timelineEvent2 := psjResult.CreateMessageEvent(t, "charlie", nil)

		// dedicated get_missing_event handler for timelineEvent2.
		// we grudgingly return a single event.
		handleGetMissingEventsRequests(t, server, serverRoom,
			[]string{timelineEvent2.EventID()}, []*gomatrixserverlib.Event{timelineEvent1},
		)

		// dedicated state_ids and state handlers for timelineEvent1's prev event (ie, the last outlier event)
		handleStateIdsRequests(t, server, serverRoom, outliers[len(outliers)-1].EventID(),
			serverRoom.AllCurrentState(), nil, nil)
		handleStateRequests(t, server, serverRoom, outliers[len(outliers)-1].EventID(),
			serverRoom.AllCurrentState(), nil, nil)

		// now, send over the most recent event, which will make the server get_missing_events
		// (we will send timelineEvent1), and then request state (we will send all the outliers).
		server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{timelineEvent2.JSON()}, nil)

		t.Logf("Charlie sent timeline event 2")
		// wait for it to become visible, which implies that all the outliers have been pulled in.
		awaitEventViaSync(t, alice, serverRoom.RoomID, timelineEvent2.EventID(), syncToken)

		// now we send over all the other events in the gap.
		server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{lateEvent.JSON()}, nil)
		t.Logf("Charlie sent late event")

		for i := 0; i < len(outliers); {
			var transactionEvents []json.RawMessage
			// a transaction can contain max 50 events
			for j := i; j < i+50 && j < len(outliers); j++ {
				transactionEvents = append(transactionEvents, outliers[j].JSON())
			}
			server.MustSendTransaction(t, deployment, "hs1", transactionEvents, nil)
			t.Logf("Charlie sent %d ex-outliers", len(transactionEvents))
			i += len(transactionEvents)
		}

		// wait for the outliers to arrive
		for i := 0; i < len(outliers); i += 10 {
			awaitEventArrival(t, 5*time.Second, alice, serverRoom.RoomID, outliers[i].EventID())
		}
		// ...and wait for the last outlier to arrive
		awaitEventArrival(t, 5*time.Second, alice, serverRoom.RoomID, outliers[len(outliers)-1].EventID())

		// release the federation /state response
		psjResult.FinishStateRequest()

		// alice should be able to sync the room. We can't use SyncJoinedTo here because that looks for the
		// membership event in the response (which we won't see, because all of the outlier events).
		// instead let's just check for the presence of the room in the timeline
		alice.MustSyncUntil(t,
			client.SyncReq{},
			func(clientUserID string, topLevelSyncJSON gjson.Result) error {
				key := "rooms.join." + client.GjsonEscape(serverRoom.RoomID) + ".timeline.events"
				array := topLevelSyncJSON.Get(key)
				if !array.Exists() {
					return fmt.Errorf("Key %s does not exist", key)
				}
				if !array.IsArray() {
					return fmt.Errorf("Key %s exists but it isn't an array", key)
				}
				return nil
			},
		)
		t.Logf("Alice successfully synced")
	})

	// test that any rejected events that are sent during the partial-state phase
	// do not suddenly become un-rejected during the resync
	t.Run("Rejected events remain rejected after resync", func(t *testing.T) {
		alice := deployment.RegisterUser(t, "hs1", "t15alice", "secret", false)
		syncToken := getSyncToken(t, alice)

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		// the HS will make an /event_auth request for the event
		federation.HandleEventAuthRequests()(server)

		// derek sends a state event, despite not having permission to send state. This should be rejected.
		badStateEvent := server.MustCreateEvent(t, serverRoom, b.Event{
			Type:     "m.room.test",
			StateKey: b.Ptr(""),
			Sender:   server.UserID("derek"),
			Content: map[string]interface{}{
				"body": "bad state event",
			},
		})
		// add to the timeline, but not the state (so that when testReceiveEventDuringPartialStateJoin checks the state,
		// it doesn't expect to see this)
		serverRoom.Timeline = append(serverRoom.Timeline, badStateEvent)
		serverRoom.Depth = badStateEvent.Depth()
		serverRoom.ForwardExtremities = []string{badStateEvent.EventID()}
		t.Logf("derek created bad state event %s", badStateEvent.EventID())

		// we also create a regular event which should be accepted, to act as a sentinel
		sentinelEvent := psjResult.CreateMessageEvent(t, "charlie", nil)
		serverRoom.AddEvent(sentinelEvent)
		t.Logf("charlie created sentinel event %s", sentinelEvent.EventID())

		server.MustSendTransaction(t, deployment, "hs1",
			[]json.RawMessage{badStateEvent.JSON(), sentinelEvent.JSON()}, nil)

		// wait for the sentinel event to be visible
		syncToken = awaitEventViaSync(t, alice, serverRoom.RoomID, sentinelEvent.EventID(), syncToken)

		// ... and check that the bad state event is *not* visible
		must.MatchResponse(t,
			alice.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", serverRoom.RoomID, "event", badStateEvent.EventID()}),
			match.HTTPResponse{
				StatusCode: 404,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_NOT_FOUND"),
				},
			},
		)

		// one more (non-state) event, for testReceiveEventDuringPartialStateJoin
		event := psjResult.CreateMessageEvent(t, "charlie", nil)
		t.Logf("charlie created regular timeline event %s", event.EventID())
		testReceiveEventDuringPartialStateJoin(t, deployment, alice, psjResult, event, syncToken)

		// check that the bad state event is *still* not visible
		must.MatchResponse(t,
			alice.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", serverRoom.RoomID, "event", badStateEvent.EventID()}),
			match.HTTPResponse{
				StatusCode: 404,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_NOT_FOUND"),
				},
			},
		)
	})

	t.Run("State accepted incorrectly", func(t *testing.T) {
		alice := deployment.RegisterUser(t, "hs1", "t16alice", "secret", false)
		syncToken := getSyncToken(t, alice)
		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()

		// the HS will make an /event_auth request for the event
		federation.HandleEventAuthRequests()(server)

		// create the room on the complement server, with charlie as the founder, and derek as a user with permission
		// to send state. He later leaves.
		roomVer := alice.GetDefaultRoomVersion(t)
		charlie := server.UserID("charlie")
		derek := server.UserID("derek")
		initialRoomEvents := federation.InitialRoomEvents(roomVer, charlie)
		// update the users map in the PL event
		for _, ev := range initialRoomEvents {
			if ev.Type == "m.room.power_levels" {
				ev.Content["users"] = map[string]int64{charlie: 100, derek: 50}
			}
		}
		serverRoom := server.MustMakeRoom(t, roomVer, initialRoomEvents)

		// derek joins
		derekJoinEvent := createJoinEvent(t, server, serverRoom, derek)
		serverRoom.AddEvent(derekJoinEvent)

		// ... and leaves again
		derekLeaveEvent := createLeaveEvent(t, server, serverRoom, derek)
		serverRoom.AddEvent(derekLeaveEvent)

		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		// derek now sends a state event with auth_events that say he was in the room. It will be
		// accepted during the faster join, but should then ultimately be rejected.
		badStateEvent := server.MustCreateEvent(t, serverRoom, b.Event{
			Type:     "m.room.test",
			StateKey: b.Ptr(""),
			Sender:   derek,
			Content: map[string]interface{}{
				"body": "bad state event",
			},
			AuthEvents: serverRoom.EventIDsOrReferences([]*gomatrixserverlib.Event{
				serverRoom.CurrentState("m.room.create", ""),
				serverRoom.CurrentState("m.room.power_levels", ""),
				derekJoinEvent,
			}),
		})
		// add to the timeline, but not the state (so that when testReceiveEventDuringPartialStateJoin checks the state,
		// it doesn't expect to see this)
		serverRoom.Timeline = append(serverRoom.Timeline, badStateEvent)
		serverRoom.Depth = badStateEvent.Depth()
		serverRoom.ForwardExtremities = []string{badStateEvent.EventID()}
		t.Logf("derek created bad state event %s with auth events %#v", badStateEvent.EventID(), badStateEvent.AuthEventIDs())
		server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{badStateEvent.JSON()}, nil)

		// the bad state event should be visible at this point
		syncToken = awaitEventViaSync(t, alice, serverRoom.RoomID, badStateEvent.EventID(), syncToken)

		// now finish up the partial join.
		event := psjResult.CreateMessageEvent(t, "charlie", nil)
		t.Logf("charlie created regular timeline event %s", event.EventID())
		testReceiveEventDuringPartialStateJoin(t, deployment, alice, psjResult, event, syncToken)

		// the bad state event should now *not* be visible
		must.MatchResponse(t,
			alice.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", serverRoom.RoomID, "event", badStateEvent.EventID()}),
			match.HTTPResponse{
				StatusCode: 404,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_NOT_FOUND"),
				},
			},
		)
	})

	t.Run("State rejected incorrectly", func(t *testing.T) {
		alice := deployment.RegisterUser(t, "hs1", "t17alice", "secret", false)
		syncToken := getSyncToken(t, alice)
		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()

		// the HS will make an /event_auth request for the event
		federation.HandleEventAuthRequests()(server)

		// create the room on the complement server, with charlie as the founder, derek as a user with permission
		// to kick users, and elsie as a bystander who has permission to send state.
		roomVer := alice.GetDefaultRoomVersion(t)
		charlie := server.UserID("charlie")
		derek := server.UserID("derek")
		elsie := server.UserID("elsie")
		initialRoomEvents := federation.InitialRoomEvents(roomVer, charlie)
		// update the users map in the PL event
		for _, ev := range initialRoomEvents {
			if ev.Type == "m.room.power_levels" {
				ev.Content["users"] = map[string]int64{charlie: 100, derek: 100, elsie: 50}
			}
		}
		serverRoom := server.MustMakeRoom(t, roomVer, initialRoomEvents)

		// derek joins
		derekJoinEvent := createJoinEvent(t, server, serverRoom, derek)
		serverRoom.AddEvent(derekJoinEvent)

		// ... and leaves again
		derekLeaveEvent := createLeaveEvent(t, server, serverRoom, derek)
		serverRoom.AddEvent(derekLeaveEvent)

		// Elsie joins
		elsieJoinEvent := createJoinEvent(t, server, serverRoom, elsie)
		serverRoom.AddEvent(elsieJoinEvent)

		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		// Derek now kicks Elsie, with auth_events that say he was in the room. It will be
		// accepted during the faster join, but should then ultimately be rejected.
		badKickEvent := server.MustCreateEvent(t, serverRoom, b.Event{
			Type:     "m.room.member",
			StateKey: &elsie,
			Sender:   derek,
			Content:  map[string]interface{}{"membership": "leave"},
			AuthEvents: serverRoom.EventIDsOrReferences([]*gomatrixserverlib.Event{
				serverRoom.CurrentState("m.room.create", ""),
				serverRoom.CurrentState("m.room.power_levels", ""),
				derekJoinEvent,
				elsieJoinEvent,
			}),
		})
		// add to the timeline, but not the state (so that when testReceiveEventDuringPartialStateJoin checks the state,
		// it doesn't expect to see this)
		serverRoom.Timeline = append(serverRoom.Timeline, badKickEvent)
		serverRoom.Depth = badKickEvent.Depth()
		serverRoom.ForwardExtremities = []string{badKickEvent.EventID()}
		t.Logf("derek created bad kick event %s with auth events %#v", badKickEvent.EventID(), badKickEvent.AuthEventIDs())

		// elsie sends some state. This should be rejected during the faster join, but ultimately accepted.
		rejectedStateEvent := server.MustCreateEvent(t, serverRoom, b.Event{
			Type:     "m.room.test",
			StateKey: b.Ptr(""),
			Sender:   elsie,
			Content:  map[string]interface{}{"body": "rejected state"},
			AuthEvents: serverRoom.EventIDsOrReferences([]*gomatrixserverlib.Event{
				serverRoom.CurrentState("m.room.create", ""),
				serverRoom.CurrentState("m.room.power_levels", ""),
				elsieJoinEvent,
			}),
		})
		serverRoom.AddEvent(rejectedStateEvent)
		t.Logf("elsie created state event %s", rejectedStateEvent.EventID())

		// we also create a regular event which should be accepted, to act as a sentinel
		sentinelEvent := psjResult.CreateMessageEvent(t, "charlie", nil)
		serverRoom.AddEvent(sentinelEvent)
		t.Logf("charlie created sentinel event %s", sentinelEvent.EventID())

		server.MustSendTransaction(t, deployment, "hs1",
			[]json.RawMessage{badKickEvent.JSON(), rejectedStateEvent.JSON(), sentinelEvent.JSON()}, nil)

		// the bad kick event should be visible at this point
		awaitEventViaSync(t, alice, serverRoom.RoomID, badKickEvent.EventID(), syncToken)

		// ... but the rejected state event should not.
		syncToken = awaitEventViaSync(t, alice, serverRoom.RoomID, sentinelEvent.EventID(), syncToken)
		must.MatchResponse(t,
			alice.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", serverRoom.RoomID, "event", rejectedStateEvent.EventID()}),
			match.HTTPResponse{
				StatusCode: 404,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_NOT_FOUND"),
				},
			},
		)

		// now finish up the partial join.
		event := psjResult.CreateMessageEvent(t, "charlie", nil)
		t.Logf("charlie created regular timeline event %s", event.EventID())
		testReceiveEventDuringPartialStateJoin(t, deployment, alice, psjResult, event, syncToken)

		// the bad kick event should now *not* be visible
		must.MatchResponse(t,
			alice.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", serverRoom.RoomID, "event", badKickEvent.EventID()}),
			match.HTTPResponse{
				StatusCode: 404,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_NOT_FOUND"),
				},
			},
		)
	})

	// when the server is in the middle of a partial state join, it should not accept
	// /make_join because it can't give a full answer.
	t.Run("Rejects make_join during partial join", func(t *testing.T) {
		// In this test, we have 3 homeservers:
		//   hs1 (the server under test) with @t18alice:hs1
		//     This is the server that will be in the middle of a partial join.
		//   testServer1 (a Complement test server) with @bob:<server name>
		//     This is the server that created the room originally.
		//   testServer2 (another Complement test server) with @charlie:<server name>
		//     This is the server that will try to make a join via testServer1.
		alice := deployment.RegisterUser(t, "hs1", "t18alice", "secret", false)

		testServer1 := createTestServer(t, deployment)
		cancel := testServer1.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, testServer1, alice.GetDefaultRoomVersion(t))
		roomID := serverRoom.RoomID
		psjResult := beginPartialStateJoin(t, testServer1, serverRoom, alice)
		defer psjResult.Destroy(t)

		// The partial join is now in progress.
		// Let's have a new test server rock up and ask to join the room by making a
		// /make_join request.

		testServer2 := createTestServer(t, deployment)
		cancel2 := testServer2.Listen()
		defer cancel2()

		fedClient2 := testServer2.FederationClient(deployment)

		// charlie sends a make_join
		_, err := fedClient2.MakeJoin(context.Background(), gomatrixserverlib.ServerName(testServer2.ServerName()), "hs1", roomID, testServer2.UserID("charlie"), federation.SupportedRoomVersions())

		if err == nil {
			t.Errorf("MakeJoin returned 200, want 404")
		} else if httpError, ok := err.(gomatrix.HTTPError); ok {
			t.Logf("MakeJoin => %d/%s", httpError.Code, string(httpError.Contents))
			if httpError.Code != 404 {
				t.Errorf("expected 404, got %d", httpError.Code)
			}
			errcode := must.GetJSONFieldStr(t, httpError.Contents, "errcode")
			if errcode != "M_NOT_FOUND" {
				t.Errorf("errcode: got %s, want M_NOT_FOUND", errcode)
			}
		} else {
			t.Errorf("MakeJoin: non-HTTPError: %v", err)
		}
	})

	// when the server is in the middle of a partial state join, it should not accept
	// /send_join because it can't give a full answer.
	t.Run("Rejects send_join during partial join", func(t *testing.T) {
		// In this test, we have 3 homeservers:
		//   hs1 (the server under test) with @t19alice:hs1
		//     This is the server that will be in the middle of a partial join.
		//   testServer1 (a Complement test server) with @charlie:<server name>
		//     This is the server that will create the room originally.
		//   testServer2 (another Complement test server) with @daniel:<server name>
		//     This is the server that will try to join the room via hs2,
		//     but only after using hs1 to /make_join (as otherwise we have no way
		//     of being able to build a request to /send_join)
		//
		alice := deployment.RegisterUser(t, "hs1", "t19alice", "secret", false)

		testServer1 := createTestServer(t, deployment)
		cancel := testServer1.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, testServer1, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, testServer1, serverRoom, alice)
		defer psjResult.Destroy(t)

		// hs1's partial join is now in progress.
		// Let's have a test server rock up and ask to /send_join in the room via hs1.
		// To do that, we need to /make_join first.
		// Asking hs1 to /make_join won't work, because it should reject that request.
		// To work around that, we /make_join via hs2.

		testServer2 := createTestServer(t, deployment)
		cancel2 := testServer2.Listen()
		defer cancel2()

		fedClient2 := testServer2.FederationClient(deployment)

		// Manually /make_join via testServer1.
		// This is permissible because testServer1 is fully joined to the room.
		// We can't actually use /make_join because host.docker.internal doesn't resolve,
		// so compute it without making any requests:
		makeJoinResp, err := federation.MakeRespMakeJoin(testServer1, serverRoom, testServer2.UserID("daniel"))
		if err != nil {
			t.Fatalf("MakeRespMakeJoin failed : %s", err)
		}

		// daniel then tries to /send_join via the homeserver under test
		joinEvent, err := makeJoinResp.JoinEvent.Build(time.Now(), gomatrixserverlib.ServerName(testServer2.ServerName()), testServer2.KeyID, testServer2.Priv, makeJoinResp.RoomVersion)
		must.NotError(t, "JoinEvent.Build", err)

		// SendJoin should return a 404 because the homeserver under test has not
		// finished its partial join.
		_, err = fedClient2.SendJoin(context.Background(), gomatrixserverlib.ServerName(testServer2.ServerName()), "hs1", joinEvent)
		if err == nil {
			t.Errorf("SendJoin returned 200, want 404")
		} else if httpError, ok := err.(gomatrix.HTTPError); ok {
			t.Logf("SendJoin => %d/%s", httpError.Code, string(httpError.Contents))
			if httpError.Code != 404 {
				t.Errorf("expected 404, got %d", httpError.Code)
			}
			errcode := must.GetJSONFieldStr(t, httpError.Contents, "errcode")
			if errcode != "M_NOT_FOUND" {
				t.Errorf("errcode: got %s, want M_NOT_FOUND", errcode)
			}
		} else {
			t.Errorf("SendJoin: non-HTTPError: %v", err)
		}
	})

	// test that a /joined_members request made during a partial-state /send_join
	// request blocks until the state is correctly synced.
	t.Run("joined_members blocks during partial state join", func(t *testing.T) {
		alice := deployment.RegisterUser(t, "hs1", "t20alice", "secret", false)

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		// Alice has now joined the room, and the server is syncing the state in the background.

		// attempts to sync should now block. Fire off a goroutine to try it.
		jmResponseChan := make(chan *http.Response)
		go func() {
			response := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", serverRoom.RoomID, "joined_members"})
			jmResponseChan <- response
			close(jmResponseChan)
		}()

		// wait for the state_ids request to arrive
		psjResult.AwaitStateIdsRequest(t)

		// the client-side requests should still be waiting
		select {
		case <-jmResponseChan:
			t.Fatalf("/joined_members completed before state resync complete. Expected it to block.")
		default:
		}

		// release the federation /state response
		psjResult.FinishStateRequest()

		// the /joined_members request should now complete, with the new room
		var jmRes *http.Response
		select {
		case <-time.After(1 * time.Second):
			t.Fatalf("/joined_members request request did not complete. Expected it to complete.")
		case jmRes = <-jmResponseChan:
		}

		derekUserID := client.GjsonEscape(server.UserID("derek"))

		must.MatchResponse(t, jmRes, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyPresent("joined"),
				match.JSONKeyPresent("joined." + alice.UserID),
				match.JSONKeyPresent("joined." + alice.UserID + ".display_name"),
				match.JSONKeyPresent("joined." + alice.UserID + ".avatar_url"),
				match.JSONKeyPresent("joined." + derekUserID),
				match.JSONKeyPresent("joined." + derekUserID + ".display_name"),
				match.JSONKeyPresent("joined." + derekUserID + ".avatar_url"),
			},
		})
	})

	// when the server is in the middle of a partial state join, it should not accept
	// /make_knock because it can't give a full answer.
	t.Run("Rejects make_knock during partial join", func(t *testing.T) {
		// In this test, we have 3 homeservers:
		//   hs1 (the server under test) with @t21alice:hs1
		//     This is the server that will be in the middle of a partial join.
		//   testServer1 (a Complement test server) with @bob:<server name>
		//     This is the server that created the room originally.
		//   testServer2 (another Complement test server) with @charlie:<server name>
		//     This is the server that will try to make a knock via testServer1.
		alice := deployment.RegisterUser(t, "hs1", "t21alice", "secret", false)

		testServer1 := createTestServer(t, deployment)
		cancel := testServer1.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, testServer1, alice.GetDefaultRoomVersion(t))
		roomID := serverRoom.RoomID
		psjResult := beginPartialStateJoin(t, testServer1, serverRoom, alice)
		defer psjResult.Destroy(t)

		// The partial join is now in progress.
		// Let's have a new test server rock up and ask to join the room by making a
		// /make_knock request.

		testServer2 := createTestServer(t, deployment)
		cancel2 := testServer2.Listen()
		defer cancel2()

		fedClient2 := testServer2.FederationClient(deployment)

		// charlie sends a make_knock
		_, err := fedClient2.MakeKnock(context.Background(), gomatrixserverlib.ServerName(testServer2.ServerName()), "hs1", roomID, testServer2.UserID("charlie"), federation.SupportedRoomVersions())

		if err == nil {
			t.Errorf("MakeKnock returned 200, want 404")
		} else if httpError, ok := err.(gomatrix.HTTPError); ok {
			t.Logf("MakeKnock => %d/%s", httpError.Code, string(httpError.Contents))
			if httpError.Code != 404 {
				t.Errorf("expected 404, got %d", httpError.Code)
			}
			errcode := must.GetJSONFieldStr(t, httpError.Contents, "errcode")
			if errcode != "M_NOT_FOUND" {
				t.Errorf("errcode: got %s, want M_NOT_FOUND", errcode)
			}
		} else {
			t.Errorf("MakeKnock: non-HTTPError: %v", err)
		}
	})

	// when the server is in the middle of a partial state join, it should not accept
	// /send_knock because it can't give a full answer.
	t.Run("Rejects send_knock during partial join", func(t *testing.T) {
		// In this test, we have 3 homeservers:
		//   hs1 (the server under test) with @t22alice:hs1
		//     This is the server that will be in the middle of a partial join.
		//   testServer1 (a Complement test server) with @charlie:<server name>
		//     This is the server that will create the room originally.
		//   testServer2 (another Complement test server) with @daniel:<server name>
		//     This is the server that will try to knock on the room via hs2,
		//     but only after using hs1 to /make_knock (as otherwise we have no way
		//     of being able to build a request to /send_knock)
		//
		alice := deployment.RegisterUser(t, "hs1", "t22alice", "secret", false)

		testServer1 := createTestServer(t, deployment)
		cancel := testServer1.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, testServer1, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, testServer1, serverRoom, alice)
		defer psjResult.Destroy(t)

		// hs1's partial join is now in progress.
		// Let's have a test server rock up and ask to /send_knock in the room via hs1.
		// To do that, we need to /make_knock first.
		// Asking hs1 to /make_knock won't work, because it should reject that request.
		// To work around that, we /make_knock via hs2.

		testServer2 := createTestServer(t, deployment)
		cancel2 := testServer2.Listen()
		defer cancel2()

		fedClient2 := testServer2.FederationClient(deployment)

		// Manually /make_knock via testServer1.
		// This is permissible because testServer1 is fully joined to the room.
		// We can't actually use /make_knock because host.docker.internal doesn't resolve,
		// so compute it without making any requests:
		makeKnockResp, err := federation.MakeRespMakeKnock(testServer1, serverRoom, testServer2.UserID("daniel"))
		if err != nil {
			t.Fatalf("MakeRespMakeKnock failed : %s", err)
		}

		// daniel then tries to /send_knock via the homeserver under test
		knockEvent, err := makeKnockResp.KnockEvent.Build(time.Now(), gomatrixserverlib.ServerName(testServer2.ServerName()), testServer2.KeyID, testServer2.Priv, makeKnockResp.RoomVersion)
		must.NotError(t, "KnockEvent.Build", err)

		// SendKnock should return a 404 because the homeserver under test has not
		// finished its partial join.
		_, err = fedClient2.SendKnock(context.Background(), gomatrixserverlib.ServerName(testServer2.ServerName()), "hs1", knockEvent)
		if err == nil {
			t.Errorf("SendKnock returned 200, want 404")
		} else if httpError, ok := err.(gomatrix.HTTPError); ok {
			t.Logf("SendKnock => %d/%s", httpError.Code, string(httpError.Contents))
			if httpError.Code != 404 {
				t.Errorf("expected 404, got %d", httpError.Code)
			}
			errcode := must.GetJSONFieldStr(t, httpError.Contents, "errcode")
			if errcode != "M_NOT_FOUND" {
				t.Errorf("errcode: got %s, want M_NOT_FOUND", errcode)
			}
		} else {
			t.Errorf("SendKnock: non-HTTPError: %v", err)
		}
	})
	t.Run("Outgoing device list updates", func(t *testing.T) {
		// setupOutgoingDeviceListUpdateTest sets up two complement homeservers.
		// A room is created on the first complement server, containing only local users.
		// Returns channels for device list updates arriving at the complement homeservers, which
		// can be used with `mustReceiveDeviceListUpdate` and `mustNotReceiveDeviceListUpdate`.
		setupOutgoingDeviceListUpdateTest := func(
			t *testing.T, deployment *docker.Deployment, aliceLocalpart string,
			opts ...func(*federation.Server),
		) (
			alice *client.CSAPI, server1 *federation.Server, server2 *federation.Server,
			deviceListUpdateChannel1 chan gomatrixserverlib.DeviceListUpdateEvent,
			deviceListUpdateChannel2 chan gomatrixserverlib.DeviceListUpdateEvent,
			room *federation.ServerRoom, cleanup func(),
		) {
			alice = deployment.RegisterUser(t, "hs1", aliceLocalpart, "secret", false)

			deviceListUpdateChannel1 = make(chan gomatrixserverlib.DeviceListUpdateEvent, 10)
			deviceListUpdateChannel2 = make(chan gomatrixserverlib.DeviceListUpdateEvent, 10)

			createDeviceListUpdateTestServer := func(
				t *testing.T, deployment *docker.Deployment,
				deviceListUpdateChannel chan gomatrixserverlib.DeviceListUpdateEvent,
				opts ...func(*federation.Server),
			) *federation.Server {
				return createTestServer(t, deployment,
					append(
						opts, // `opts` goes first so that it can override any of the following handlers
						federation.HandleEventAuthRequests(),
						federation.HandleTransactionRequests(
							func(e *gomatrixserverlib.Event) {
								t.Fatalf("Received unexpected PDU: %s", string(e.JSON()))
							},
							func(e gomatrixserverlib.EDU) {
								if e.Type == "m.presence" {
									return
								}
								if e.Type != "m.device_list_update" {
									t.Fatalf("Received unexpected EDU: %s", e)
								}

								t.Logf("Complement server received m.device_list_update: %v", string(e.Content))
								var deviceListUpdate gomatrixserverlib.DeviceListUpdateEvent
								json.Unmarshal(e.Content, &deviceListUpdate)
								deviceListUpdateChannel <- deviceListUpdate
							},
						),
					)...,
				)
			}

			server1 = createDeviceListUpdateTestServer(t, deployment, deviceListUpdateChannel1, opts...)
			server2 = createDeviceListUpdateTestServer(t, deployment, deviceListUpdateChannel2, opts...)
			cancel1 := server1.Listen()
			cancel2 := server2.Listen()

			room = createTestRoom(t, server1, alice.GetDefaultRoomVersion(t))

			cleanup = func() {
				cancel1()
				cancel2()
				close(deviceListUpdateChannel1)
				close(deviceListUpdateChannel2)
			}
			return
		}

		// renameDevice triggers an outgoing device list update
		// We may want to rewrite this to update keys instead in the future.
		renameDevice := func(t *testing.T, user *client.CSAPI, displayName string) {
			t.Helper()

			user.MustDoFunc(
				t,
				"PUT",
				[]string{"_matrix", "client", "v3", "devices", user.DeviceID},
				client.WithJSONBody(
					t,
					map[string]interface{}{
						"display_name": displayName,
					},
				),
			)

			t.Logf("%s sent device list update.", user.UserID)
		}

		// mustReceiveDeviceListUpdate checks that a complement homeserver has received a device
		// list update since the last call. Only consumes a single device list update.
		mustReceiveDeviceListUpdate := func(
			t *testing.T, channel chan gomatrixserverlib.DeviceListUpdateEvent, errFormat string,
			args ...interface{},
		) {
			t.Helper()

			select {
			case <-time.After(1 * time.Second):
				t.Fatalf(errFormat, args...)
			case <-channel:
			}
		}

		// mustNotReceiveDeviceListUpdate checks that a complement homeserver has not received a
		// device list update since the last call.
		mustNotReceiveDeviceListUpdate := func(
			t *testing.T, channel chan gomatrixserverlib.DeviceListUpdateEvent, errFormat string,
			args ...interface{},
		) {
			t.Helper()

			select {
			case <-time.After(1 * time.Second):
			case <-channel:
				t.Fatalf(errFormat, args...)
			}
		}

		// test that device list updates are sent to the remote homeservers listed in the
		// `/send_join` response in a room with partial state.
		t.Run("Device list updates reach all servers in partial state rooms", func(t *testing.T) {
			alice, server1, server2, deviceListUpdateChannel1, deviceListUpdateChannel2, room, cleanup := setupOutgoingDeviceListUpdateTest(t, deployment, "t23alice")
			defer cleanup()

			// The room starts with @charlie:server1 and @derek:server1 in it.
			// @elsie:server2 joins the room before @t23alice:hs1.
			room.AddEvent(createJoinEvent(t, server2, room, server2.UserID("elsie")))

			// @t23alice:hs1 joins the room.
			psjResult := beginPartialStateJoin(t, server1, room, alice)
			defer psjResult.Destroy(t)

			// Both homeservers should receive device list updates.
			renameDevice(t, alice, "A new device name 1")
			mustReceiveDeviceListUpdate(t, deviceListUpdateChannel1, "@charlie and @derek did not receive device list update.")
			mustReceiveDeviceListUpdate(t, deviceListUpdateChannel2, "@elsie did not receive device list update.")
			t.Log("@charlie, @derek and @elsie received device list update.")

			// Finish the partial state join.
			psjResult.FinishStateRequest()
			awaitPartialStateJoinCompletion(t, room, alice)

			// Both homeservers should still receive device list updates.
			renameDevice(t, alice, "A new device name 2")
			mustReceiveDeviceListUpdate(t, deviceListUpdateChannel1, "@charlie and @derek did not receive device list update.")
			mustReceiveDeviceListUpdate(t, deviceListUpdateChannel2, "@elsie did not receive device list update.")
			t.Log("@charlie, @derek and @elsie received device list update.")
		})

		// test that device list updates are additionally sent to remote homeservers that join after
		// the local homeserver.
		t.Run("Device list updates reach newly joined servers in partial state rooms", func(t *testing.T) {
			alice, server1, server2, deviceListUpdateChannel1, deviceListUpdateChannel2, room, cleanup := setupOutgoingDeviceListUpdateTest(t, deployment, "t24alice")
			defer cleanup()

			// The room starts with @charlie:server1 and @derek:server1 in it.
			// @t24alice:hs1 joins the room.
			psjResult := beginPartialStateJoin(t, server1, room, alice)
			defer psjResult.Destroy(t)

			// Only server1 should receive device list updates.
			renameDevice(t, alice, "A new device name 1")
			mustReceiveDeviceListUpdate(t, deviceListUpdateChannel1, "@charlie and @derek did not receive device list update.")
			mustNotReceiveDeviceListUpdate(t, deviceListUpdateChannel2, "@elsie received device list update unexpectedly.")
			t.Log("@charlie and @derek received device list update.")

			// @elsie:server2 joins the room.
			// Make server1 send the event to the homeserver, since server2's rooms list isn't set
			// up right and it can't answer queries about events in the room.
			joinEvent := createJoinEvent(t, server2, room, server2.UserID("elsie"))
			room.AddEvent(joinEvent)
			server1.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{joinEvent.JSON()}, nil)
			awaitEventViaSync(t, alice, room.RoomID, joinEvent.EventID(), "")

			// Both servers should receive device list updates now.
			renameDevice(t, alice, "A new device name 2")
			mustReceiveDeviceListUpdate(t, deviceListUpdateChannel1, "@charlie and @derek did not receive device list update.")
			mustReceiveDeviceListUpdate(t, deviceListUpdateChannel2, "@elsie did not receive device list update.")
			t.Log("@charlie, @derek and @elsie received device list update.")

			// Finish the partial state join.
			psjResult.FinishStateRequest()
			awaitPartialStateJoinCompletion(t, room, alice)

			// Both homeservers should still receive device list updates.
			renameDevice(t, alice, "A new device name 3")
			mustReceiveDeviceListUpdate(t, deviceListUpdateChannel1, "@charlie and @derek did not receive device list update.")
			mustReceiveDeviceListUpdate(t, deviceListUpdateChannel2, "@elsie did not receive device list update.")
			t.Log("@charlie, @derek and @elsie received device list update.")
		})

		// test that device list updates are sent to the remote homeservers listed in the
		// `/send_join` response in a room with partial state, even after they leave. The homeserver
		// under test must do so, as it has no way of knowing that a remote homeserver has no more
		// users in the room.
		t.Run("Device list updates no longer reach departed servers after partial state join completes", func(t *testing.T) {
			alice, server1, server2, deviceListUpdateChannel1, deviceListUpdateChannel2, room, cleanup := setupOutgoingDeviceListUpdateTest(t, deployment, "t25alice")
			defer cleanup()

			// The room starts with @charlie:server1 and @derek:server1 in it.
			// @elsie:server2 joins the room before @t25alice:hs1.
			room.AddEvent(createJoinEvent(t, server2, room, server2.UserID("elsie")))

			// @t25alice:hs1 joins the room.
			psjResult := beginPartialStateJoin(t, server1, room, alice)
			defer psjResult.Destroy(t)

			// @elsie:server2 leaves the room.
			// Make server1 send the event to the homeserver, since server2's rooms list isn't set
			// up right and it can't answer queries about events in the room.
			leaveEvent := createLeaveEvent(t, server2, room, server2.UserID("elsie"))
			room.AddEvent(leaveEvent)
			server1.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{leaveEvent.JSON()}, nil)
			awaitEventViaSync(t, alice, room.RoomID, leaveEvent.EventID(), "")

			// Both homeservers should receive device list updates, since hs1 cannot know that
			// @elsie was the last user from server2 in the room.
			renameDevice(t, alice, "A new device name 1")
			mustReceiveDeviceListUpdate(t, deviceListUpdateChannel1, "@charlie and @derek did not receive device list update.")
			mustReceiveDeviceListUpdate(t, deviceListUpdateChannel2, "@elsie did not receive device list update.")
			t.Log("@charlie, @derek and @elsie received device list update.")

			// Finish the partial state join.
			psjResult.FinishStateRequest()
			awaitPartialStateJoinCompletion(t, room, alice)

			// @elsie:server2 should no longer receive device list updates.
			renameDevice(t, alice, "A new device name 2")
			mustReceiveDeviceListUpdate(t, deviceListUpdateChannel1, "@charlie and @derek did not receive device list update.")
			mustNotReceiveDeviceListUpdate(t, deviceListUpdateChannel2, "@elsie received device list update unexpectedly.")
			t.Log("@charlie and @derek received device list update.")
		})

		// setupIncorrectlyAcceptedKick joins the homeserver under test to a room, then joins
		// @elsie:server2 and sends an invalid event to kick @elsie:server2 from the room.
		// As a side effect, @derek is promoted to admin and leaves the room before the homeserver
		// under test joins.
		setupIncorrectlyAcceptedKick := func(
			t *testing.T, deployment *docker.Deployment, alice *client.CSAPI,
			server1 *federation.Server, server2 *federation.Server,
			deviceListUpdateChannel1 chan gomatrixserverlib.DeviceListUpdateEvent,
			deviceListUpdateChannel2 chan gomatrixserverlib.DeviceListUpdateEvent,
			room *federation.ServerRoom,
		) (syncToken string, psjResult partialStateJoinResult) {
			derek := server1.UserID("derek")
			elsie := server2.UserID("elsie")

			// The room starts with @charlie:server1 and @derek:server1 in it.
			// @derek:server1 becomes an admin.
			var powerLevelsContent map[string]interface{}
			json.Unmarshal(room.CurrentState("m.room.power_levels", "").Content(), &powerLevelsContent)
			powerLevelsContent["users"].(map[string]interface{})[derek] = 100
			room.AddEvent(server1.MustCreateEvent(t, room, b.Event{
				Type:     "m.room.power_levels",
				StateKey: b.Ptr(""),
				Sender:   server1.UserID("charlie"),
				Content:  powerLevelsContent,
			}))

			// @derek:server1 leaves the room.
			derekJoinEvent := room.CurrentState("m.room.member", derek)
			derekLeaveEvent := createLeaveEvent(t, server1, room, derek)
			room.AddEvent(derekLeaveEvent)

			// @alice:hs1 joins the room.
			psjResult = beginPartialStateJoin(t, server1, room, alice)

			// @elsie:server2 joins the room.
			// Make server1 send the event to the homeserver, since server2's rooms list isn't set
			// up right and it can't answer queries about events in the room.
			joinEvent := createJoinEvent(t, server2, room, elsie)
			room.AddEvent(joinEvent)
			server1.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{joinEvent.JSON()}, nil)
			syncToken = awaitEventViaSync(t, alice, room.RoomID, joinEvent.EventID(), "")

			// Both servers should receive device list updates.
			renameDevice(t, alice, "A new device name 1")
			mustReceiveDeviceListUpdate(t, deviceListUpdateChannel1, "@charlie and @derek did not receive device list update.")
			mustReceiveDeviceListUpdate(t, deviceListUpdateChannel2, "@elsie did not receive device list update.")
			t.Log("@charlie, @derek and @elsie received device list update.")

			// @derek:server1 "kicks" @elsie:server2.
			badKickEvent := server1.MustCreateEvent(t, room, b.Event{
				Type:     "m.room.member",
				StateKey: b.Ptr(elsie),
				Sender:   derek,
				Content:  map[string]interface{}{"membership": "leave"},
				AuthEvents: room.EventIDsOrReferences([]*gomatrixserverlib.Event{
					room.CurrentState("m.room.create", ""),
					room.CurrentState("m.room.power_levels", ""),
					derekJoinEvent,
				}),
			})
			room.Timeline = append(room.Timeline, badKickEvent)
			room.Depth = badKickEvent.Depth()
			room.ForwardExtremities = []string{badKickEvent.EventID()}
			server1.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{badKickEvent.JSON()}, nil)
			awaitEventViaSync(t, alice, room.RoomID, badKickEvent.EventID(), syncToken)

			return syncToken, psjResult
		}

		// setupAnotherSharedRoomThenLeave has @alice:hs1 create a public room, @elsie:server2 join
		// the public room, then leave the partial state room.
		// Returns @alice:hs1's sync token after @elsie:server2 has left the partial state room.
		setupAnotherSharedRoomThenLeave := func(
			t *testing.T, deployment *docker.Deployment, alice *client.CSAPI,
			server1 *federation.Server, server2 *federation.Server,
			partialStateRoom *federation.ServerRoom, syncToken string,
		) string {
			elsie := server2.UserID("elsie")

			// @alice:hs1 creates a public room.
			roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})

			// @elsie:server2 joins the room.
			server2.MustJoinRoom(t, deployment, "hs1", roomID, elsie)
			alice.MustSyncUntil(t,
				client.SyncReq{
					Since:  syncToken,
					Filter: buildLazyLoadingSyncFilter(nil),
				},
				client.SyncJoinedTo(elsie, roomID),
			)

			// @elsie:server2 leaves the room.
			// Make server1 send the event to the homeserver, since server2's rooms list isn't set
			// up right and it can't answer queries about events in the room.
			leaveEvent := createLeaveEvent(t, server2, partialStateRoom, elsie)
			partialStateRoom.AddEvent(leaveEvent)
			server1.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{leaveEvent.JSON()}, nil)
			syncToken = awaitEventViaSync(t, alice, partialStateRoom.RoomID, leaveEvent.EventID(), syncToken)

			return syncToken
		}

		// testMissedDeviceListUpdateSentOncePartialJoinCompletes takes a room where hs1 incorrectly
		// believes @elsie:server2 not to be present and tests that server2 receives missed device
		// list updates once hs1's partial state join has completed.
		testMissedDeviceListUpdateSentOncePartialJoinCompletes := func(
			t *testing.T, deployment *docker.Deployment, alice *client.CSAPI,
			server1 *federation.Server, server2 *federation.Server,
			deviceListUpdateChannel1 chan gomatrixserverlib.DeviceListUpdateEvent,
			deviceListUpdateChannel2 chan gomatrixserverlib.DeviceListUpdateEvent,
			room *federation.ServerRoom, psjResult partialStateJoinResult, syncToken string,
			withLeave bool,
		) {
			// The homeserver under test incorrectly believes @elsie:server2 is not in the room.
			// @elsie:server2 should miss device list updates.
			renameDevice(t, alice, "A new device name 2")
			mustReceiveDeviceListUpdate(t, deviceListUpdateChannel1, "@charlie and @derek did not receive device list update.")
			mustNotReceiveDeviceListUpdate(t, deviceListUpdateChannel2, "@elsie received device list update unexpectedly.")
			t.Log("@charlie and @derek received device list update.")

			if withLeave {
				// @elsie:server2 joins a room shared with @alice:hs1 and leaves the partial state room.
				// The homeserver under test cannot simply use the current state of the room to
				// determine which device list updates it must send out once the partial state join
				// completes.
				setupAnotherSharedRoomThenLeave(t, deployment, alice, server1, server2, room, syncToken)
			}

			// Finish the partial state join.
			psjResult.FinishStateRequest()
			awaitPartialStateJoinCompletion(t, room, alice)

			// @elsie:server2 must receive missed device list updates.
			mustReceiveDeviceListUpdate(t, deviceListUpdateChannel2, "@elsie did not receive missed device list update.")
			t.Log("@elsie received missed device list update.")

			// Both homeservers should receive device list updates again.
			renameDevice(t, alice, "A new device name 3")
			mustReceiveDeviceListUpdate(t, deviceListUpdateChannel1, "@charlie and @derek did not receive device list update.")
			mustReceiveDeviceListUpdate(t, deviceListUpdateChannel2, "@elsie did not receive device list update.")
			t.Log("@charlie, @derek and @elsie received device list update.")
		}

		// test that device list updates are sent to remote homeservers incorrectly believed not to
		// be in a room with partial state once the partial state join completes.
		t.Run("Device list updates reach incorrectly kicked servers once partial state join completes", func(t *testing.T) {
			alice, server1, server2, deviceListUpdateChannel1, deviceListUpdateChannel2, room, cleanup := setupOutgoingDeviceListUpdateTest(t, deployment, "t26alice")
			defer cleanup()

			// The room starts with @charlie:server1 and @derek:server1 in it.
			// @t26alice:hs1 joins the room, followed by @elsie:server2.
			// @elsie:server2 is kicked with an invalid event.
			syncToken, psjResult := setupIncorrectlyAcceptedKick(t, deployment, alice, server1, server2, deviceListUpdateChannel1, deviceListUpdateChannel2, room)
			defer psjResult.Destroy(t)

			// @t26alice:hs1 sends out a device list update which is missed by @elsie:server2.
			// @elsie:server2 must receive missed device list updates once the partial state join finishes.
			testMissedDeviceListUpdateSentOncePartialJoinCompletes(t, deployment, alice,
				server1, server2, deviceListUpdateChannel1, deviceListUpdateChannel2, room,
				psjResult, syncToken, false,
			)
		})

		// test that device list updates are sent to remote homeservers incorrectly believed not to
		// be in a room with partial state once the partial state join completes, even if the remote
		// homeserver leaves the room beforehand.
		t.Run("Device list updates reach incorrectly kicked servers once partial state join completes even though remote server left room", func(t *testing.T) {
			alice, server1, server2, deviceListUpdateChannel1, deviceListUpdateChannel2, room, cleanup := setupOutgoingDeviceListUpdateTest(t, deployment, "t27alice")
			defer cleanup()

			// The room starts with @charlie:server1 and @derek:server1 in it.
			// @t27alice:hs1 joins the room, followed by @elsie:server2.
			// @elsie:server2 is kicked with an invalid event.
			syncToken, psjResult := setupIncorrectlyAcceptedKick(t, deployment, alice, server1, server2, deviceListUpdateChannel1, deviceListUpdateChannel2, room)
			defer psjResult.Destroy(t)

			// @t27alice:hs1 sends out a device list update which is missed by @elsie:server2.
			// @elsie:server2 joins another room shared with @t27alice:hs1 and leaves the partial state room.
			// @elsie:server2 must receive missed device list updates once the partial state join finishes.
			testMissedDeviceListUpdateSentOncePartialJoinCompletes(t, deployment, alice,
				server1, server2, deviceListUpdateChannel1, deviceListUpdateChannel2, room,
				psjResult, syncToken, true,
			)
		})

		// handleSendJoinRequestsWithIncompleteServersInRoom responds to `/send_join` requests with a minimal `servers_in_room` list.
		handleSendJoinRequestsWithIncompleteServersInRoom := func(server *federation.Server) {
			server.Mux().Handle("/_matrix/federation/v2/send_join/{roomID}/{eventID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				// Tell the joining server there are no other servers in the room.
				federation.SendJoinRequestsHandler(server, w, req, true, true)
			})).Methods("PUT")
		}

		// test that device list updates are sent to remote homeservers incorrectly omitted from the
		// `/send_join` response once the partial state join completes.
		t.Run("Device list updates reach incorrectly absent servers once partial state join completes", func(t *testing.T) {
			alice, server1, server2, deviceListUpdateChannel1, deviceListUpdateChannel2, room, cleanup := setupOutgoingDeviceListUpdateTest(
				t, deployment, "t28alice", handleSendJoinRequestsWithIncompleteServersInRoom,
			)
			defer cleanup()

			// The room starts with @charlie:server1 and @derek:server1 in it.
			// @elsie:server2 joins the room, followed by @t28alice:hs1.
			// server1 does not tell hs1 that server2 is in the room.
			room.AddEvent(createJoinEvent(t, server2, room, server2.UserID("elsie")))
			psjResult := beginPartialStateJoin(t, server1, room, alice)
			defer psjResult.Destroy(t)

			// @t28alice:hs1 sends out a device list update which is missed by @elsie:server2.
			// @elsie:server2 must receive missed device list updates once the partial state join finishes.
			testMissedDeviceListUpdateSentOncePartialJoinCompletes(t, deployment, alice,
				server1, server2, deviceListUpdateChannel1, deviceListUpdateChannel2, room,
				psjResult, "", false,
			)
		})

		// test that device list updates are sent to remote homeservers incorrectly omitted from the
		// `/send_join` response once the partial state join completes, even if the remote
		// homeserver leaves the room beforehand.
		t.Run("Device list updates reach incorrectly absent servers once partial state join completes even though remote server left room", func(t *testing.T) {
			alice, server1, server2, deviceListUpdateChannel1, deviceListUpdateChannel2, room, cleanup := setupOutgoingDeviceListUpdateTest(
				t, deployment, "t29alice", handleSendJoinRequestsWithIncompleteServersInRoom,
			)
			defer cleanup()

			// The room starts with @charlie:server1 and @derek:server1 in it.
			// @elsie:server2 joins the room, followed by @t29alice:hs1.
			// server1 does not tell hs1 that server2 is in the room.
			room.AddEvent(createJoinEvent(t, server2, room, server2.UserID("elsie")))
			psjResult := beginPartialStateJoin(t, server1, room, alice)
			defer psjResult.Destroy(t)

			// @t29alice:hs1 sends out a device list update which is missed by @elsie:server2.
			// @elsie:server2 joins another room shared with @t29alice:hs1 and leaves the partial state room.
			// @elsie:server2 must receive missed device list updates once the partial state join finishes.
			testMissedDeviceListUpdateSentOncePartialJoinCompletes(t, deployment, alice,
				server1, server2, deviceListUpdateChannel1, deviceListUpdateChannel2, room,
				psjResult, "", true,
			)
		})
	})

	// test that:
	//  * remote device lists are correctly cached or not cached
	//  * local users are told about potential device list changes in `/sync`'s
	//    `device_lists.changed/left`
	//  * local users are told about potential device list changes in `/keys/changes`.
	t.Run("Device list tracking", func(t *testing.T) {
		// setupDeviceListCachingTest sets up a complement homeserver.
		// A room is created on the complement server, containing only local users.
		// Returns a channel for device list requests arriving at the complement homeserver, which
		// can be used with `mustQueryKeysWithFederationRequest` and
		// `mustQueryKeysWithoutFederationRequest`.
		setupDeviceListCachingTest := func(
			t *testing.T, deployment *docker.Deployment, aliceLocalpart string,
		) (
			alice *client.CSAPI, server *federation.Server, userDevicesQueryChannel chan string,
			room *federation.ServerRoom, sendDeviceListUpdate func(string), cleanup func(),
		) {
			alice = deployment.RegisterUser(t, "hs1", aliceLocalpart, "secret", false)

			userDevicesQueryChannel = make(chan string, 1)

			makeRespUserDeviceKeys := func(
				userID string, deviceID string,
			) gomatrixserverlib.RespUserDeviceKeys {
				return gomatrixserverlib.RespUserDeviceKeys{
					UserID:   userID,
					DeviceID: deviceID,
					Algorithms: []string{
						"m.megolm.v1.aes-sha2",
					},
					Keys: map[gomatrixserverlib.KeyID]gomatrixserverlib.Base64Bytes{
						"ed25519:JLAFKJWSCS": []byte("lEuiRJBit0IG6nUf5pUzWTUEsRVVe/HJkoKuEww9ULI"),
					},
					Signatures: map[string]map[gomatrixserverlib.KeyID]gomatrixserverlib.Base64Bytes{
						userID: {
							"ed25519:JLAFKJWSCS": []byte("dSO80A01XiigH3uBiDVx/EjzaoycHcjq9lfQX0uWsqxl2giMIiSPR8a4d291W1ihKJL/a+myXS367WT6NAIcBA"),
						},
					},
				}
			}

			lastDeviceStreamID := int64(2)
			server = createTestServer(t, deployment,
				federation.HandleEventAuthRequests(),
				func(server *federation.Server) {
					server.Mux().HandleFunc("/_matrix/federation/v1/user/devices/{userID}",
						http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
							t.Logf("Incoming %s %s", req.Method, req.URL.Path)

							vars := mux.Vars(req)
							userID := vars["userID"]
							deviceID := fmt.Sprintf("%s_device", userID)

							userDevicesQueryChannel <- userID

							// Make up a device list for the user.
							responseBytes, _ := json.Marshal(gomatrixserverlib.RespUserDevices{
								UserID:   userID,
								StreamID: lastDeviceStreamID,
								Devices: []gomatrixserverlib.RespUserDevice{
									{
										DeviceID:    deviceID,
										DisplayName: fmt.Sprintf("%s's device", userID),
										Keys:        makeRespUserDeviceKeys(userID, deviceID),
									},
								},
							})
							w.WriteHeader(200)
							w.Write(responseBytes)
						}),
					).Methods("GET")
				},
				func(server *federation.Server) {
					server.Mux().HandleFunc("/_matrix/federation/v1/user/keys/query",
						http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
							t.Logf("Incoming %s %s", req.Method, req.URL.Path)

							body, err := ioutil.ReadAll(req.Body)
							if err != nil {
								t.Fatalf("unable to read /user/keys/query request body: %s", err)
							}

							var queryKeysRequest struct {
								DeviceKeys map[string][]string `json:"device_keys"`
							}
							if err := json.Unmarshal(body, &queryKeysRequest); err != nil {
								t.Fatalf("unable to unmarshall /user/keys/query request body: %s", err)
							}

							// Make up keys for every device requested.
							deviceKeys := make(map[string]map[string]gomatrixserverlib.DeviceKeys)
							for userID := range queryKeysRequest.DeviceKeys {
								userDevicesQueryChannel <- userID

								deviceID := fmt.Sprintf("%s_device", userID)
								deviceKeys[userID] = map[string]gomatrixserverlib.DeviceKeys{
									deviceID: {
										RespUserDeviceKeys: makeRespUserDeviceKeys(userID, deviceID),
									},
								}
							}

							responseBytes, _ := json.Marshal(gomatrixserverlib.RespQueryKeys{
								DeviceKeys: deviceKeys,
							})
							w.WriteHeader(200)
							w.Write(responseBytes)
						}),
					).Methods("POST")
				},
			)

			cancel := server.Listen()

			room = createTestRoom(t, server, alice.GetDefaultRoomVersion(t))

			sendDeviceListUpdate = func(localpart string) {
				t.Helper()

				userID := server.UserID(localpart)
				deviceID := fmt.Sprintf("%s_device", userID)

				// Advance the stream ID by 2 each time, so that the homeserver under test thinks it
				// has missed an update and is forced to make a federation request to request the
				// updated device list.
				lastDeviceStreamID += 2

				keys, _ := json.Marshal(makeRespUserDeviceKeys(userID, deviceID))
				deviceListUpdate, _ := json.Marshal(gomatrixserverlib.DeviceListUpdateEvent{
					UserID:            userID,
					DeviceID:          deviceID,
					DeviceDisplayName: fmt.Sprintf("%s's device", userID),
					StreamID:          lastDeviceStreamID,
					PrevID:            []int64{lastDeviceStreamID - 1},
					Deleted:           false,
					Keys:              keys,
				})
				server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{}, []gomatrixserverlib.EDU{
					{
						Type:        "m.device_list_update",
						Origin:      server.ServerName(),
						Destination: "hs1",
						Content:     deviceListUpdate,
					},
				})
			}

			cleanup = func() {
				cancel()
				close(userDevicesQueryChannel)
			}
			return
		}

		// mustQueryKeys makes a /keys/query request to the homeserver under test.
		mustQueryKeys := func(t *testing.T, user *client.CSAPI, userID string) {
			t.Helper()

			user.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "keys", "query"},
				client.WithJSONBody(t, map[string]interface{}{
					"device_keys": map[string]interface{}{
						userID: []string{},
					},
				}),
			)
		}

		// mustQueryKeysWithFederationRequest makes a /keys/query request to the homeserver under
		// test and checks that the complement homeserver has received a device list request since
		// the previous call to `mustQueryKeysWithFederationRequest` or
		// `mustQueryKeysWithoutFederationRequest`.
		// Accepts the channel for device list requests returned by `setupDeviceListCachingTest`.
		mustQueryKeysWithFederationRequest := func(
			t *testing.T, user *client.CSAPI, userDevicesQueryChannel chan string, userID string,
		) {
			t.Helper()

			mustQueryKeys(t, user, userID)

			if len(userDevicesQueryChannel) == 0 {
				t.Fatalf("%s's device list was cached when it should not be.", userID)
			}

			// Empty the channel.
			for len(userDevicesQueryChannel) > 0 {
				<-userDevicesQueryChannel
			}
		}

		// mustQueryKeysWithoutFederationRequest makes a /keys/query request to the homeserver under
		// test and checks that the complement homeserver has not received a device list request
		// since the previous call to `mustQueryKeysWithFederationRequest` or
		// `mustQueryKeysWithoutFederationRequest`.
		// Accepts the channel for device list requests returned by `setupDeviceListCachingTest`.
		mustQueryKeysWithoutFederationRequest := func(
			t *testing.T, user *client.CSAPI, userDevicesQueryChannel chan string, userID string,
		) {
			t.Helper()

			mustQueryKeys(t, user, userID)

			if len(userDevicesQueryChannel) > 0 {
				t.Fatalf("%s's device list was not cached when it should have been.", userID)
			}

			// Empty the channel.
			for len(userDevicesQueryChannel) > 0 {
				<-userDevicesQueryChannel
			}
		}

		// syncDeviceListsHas checks that `device_lists.changed` or `device_lists.left` contains a
		// given user ID.
		syncDeviceListsHas := func(section string, expectedUserID string) client.SyncCheckOpt {
			jsonPath := fmt.Sprintf("device_lists.%s", section)
			return func(clientUserID string, topLevelSyncJSON gjson.Result) error {
				usersWithChangedDeviceListsArray := topLevelSyncJSON.Get(jsonPath).Array()
				for _, userID := range usersWithChangedDeviceListsArray {
					if userID.Str == expectedUserID {
						return nil
					}
				}
				return fmt.Errorf(
					"syncDeviceListsHas: %s not found in %s",
					expectedUserID,
					jsonPath,
				)
			}
		}

		// mustSyncUntilDeviceListsHas syncs until `device_lists.changed` or `device_lists.left`
		// contains a given user ID.
		// Also tests that /keys/changes returns the same information.
		mustSyncUntilDeviceListsHas := func(
			t *testing.T, user *client.CSAPI, syncToken string, section string,
			expectedUserID string,
		) string {
			t.Helper()

			nextSyncToken := user.MustSyncUntil(
				t,
				client.SyncReq{
					Since:  syncToken,
					Filter: buildLazyLoadingSyncFilter(nil),
				},
				syncDeviceListsHas(section, expectedUserID),
			)

			res := user.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "keys", "changes"},
				client.WithQueries(url.Values{
					"from": []string{syncToken},
					"to":   []string{nextSyncToken},
				}),
			)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONCheckOffAllowUnwanted(
						section,
						[]interface{}{expectedUserID},
						func(r gjson.Result) interface{} { return r.Str },
						nil,
					),
				},
			})
			return nextSyncToken
		}

		// tests device list tracking for pre-existing members in a room with partial state.
		// Tests that:
		//  * device lists are not cached for pre-existing members.
		//  * device list updates received while the room has partial state are sent to clients once
		//    fully joined.
		t.Run("Device list tracking for pre-existing members in partial state room", func(t *testing.T) {
			alice, server, userDevicesChannel, room, sendDeviceListUpdate, cleanup := setupDeviceListCachingTest(t, deployment, "t30alice")
			defer cleanup()

			// The room starts with @charlie and @derek in it.

			// @t30alice:hs1 joins the room.
			psjResult := beginPartialStateJoin(t, server, room, alice)
			defer psjResult.Destroy(t)

			// @charlie and @derek's device list ought to not be cached.
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("charlie"))
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("derek"))
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("charlie"))
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("derek"))

			// @charlie sends a message.
			// Depending on the homeserver implementation, @t30alice:hs1 may be told that @charlie's devices are being tracked.
			event := psjResult.CreateMessageEvent(t, "charlie", nil)
			psjResult.Server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{event.JSON()}, nil)
			syncToken := awaitEventViaSync(t, alice, psjResult.ServerRoom.RoomID, event.EventID(), "")

			// @charlie updates their device list.
			// Depending on the homeserver implementation, @t30alice:hs1 may or may not see the update,
			// independent of what they were told about the tracking of @charlie's device list earlier.
			sendDeviceListUpdate("charlie")

			// Before completing the partial state join, try to wait for the homeserver to finish processing the device list update.
			event = psjResult.CreateMessageEvent(t, "charlie", nil)
			psjResult.Server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{event.JSON()}, nil)
			awaitEventViaSync(t, alice, psjResult.ServerRoom.RoomID, event.EventID(), syncToken)

			// Finish the partial state join.
			psjResult.FinishStateRequest()
			awaitPartialStateJoinCompletion(t, room, alice)

			// @charlie's device list update ought to have arrived by now.
			mustSyncUntilDeviceListsHas(t, alice, syncToken, "changed", server.UserID("charlie"))

			// Cache @charlie and @derek's device lists.
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("charlie"))
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("derek"))

			// @charlie and @derek's device lists ought to be cached now.
			mustQueryKeysWithoutFederationRequest(t, alice, userDevicesChannel, server.UserID("charlie"))
			mustQueryKeysWithoutFederationRequest(t, alice, userDevicesChannel, server.UserID("derek"))
		})

		// test device list tracking when a pre-existing member in a room with partial state joins
		// another shared room and starts being tracked for real.
		t.Run("Device list tracking when pre-existing members in partial state room join another shared room", func(t *testing.T) {
			alice, server, _, room, sendDeviceListUpdate, cleanup := setupDeviceListCachingTest(t, deployment, "t31alice")
			defer cleanup()

			// The room starts with @charlie and @derek in it.

			// @t31alice:hs1 joins the room.
			psjResult := beginPartialStateJoin(t, server, room, alice)
			defer psjResult.Destroy(t)

			// @charlie sends a message.
			// Depending on the homeserver implementation, @t31alice:hs1 may be told that @charlie's devices are being tracked.
			event := psjResult.CreateMessageEvent(t, "charlie", nil)
			psjResult.Server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{event.JSON()}, nil)
			syncToken := awaitEventViaSync(t, alice, psjResult.ServerRoom.RoomID, event.EventID(), "")

			// @charlie updates their device list.
			// Depending on the homeserver implementation, @t31alice:hs1 may or may not see the update,
			// independent of what they were told about the tracking of @charlie's device list earlier.
			sendDeviceListUpdate("charlie")

			// @alice:hs1 creates a public room.
			otherRoomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})

			// @charlie joins the room.
			// Now @charlie's device list is definitely being tracked.
			server.MustJoinRoom(t, deployment, "hs1", otherRoomID, server.UserID("charlie"))
			alice.MustSyncUntil(t,
				client.SyncReq{
					Since:  syncToken,
					Filter: buildLazyLoadingSyncFilter(nil),
				},
				client.SyncJoinedTo(server.UserID("charlie"), otherRoomID),
			)

			// Depending on the homeserver implementation, @t31alice:hs1 must have been told that either:
			//  * charlie updated their device list, or
			//  * charlie's device list is being tracked now, for real.
			mustSyncUntilDeviceListsHas(t, alice, syncToken, "changed", server.UserID("charlie"))
		})

		// test device list tracking for users that join after the local homeserver.
		// It is expected that device list tracking works as normal for such users.
		t.Run("Device list tracked for new members in partial state room", func(t *testing.T) {
			alice, server, userDevicesChannel, room, sendDeviceListUpdate, cleanup := setupDeviceListCachingTest(t, deployment, "t32alice")
			defer cleanup()

			// The room starts with @charlie and @derek in it.

			// @t32alice:hs1 joins the room.
			psjResult := beginPartialStateJoin(t, server, room, alice)
			defer psjResult.Destroy(t)

			syncToken := getSyncToken(t, alice)

			// @elsie joins the room.
			joinEvent := createJoinEvent(t, server, room, server.UserID("elsie"))
			room.AddEvent(joinEvent)
			server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{joinEvent.JSON()}, nil)
			awaitEventViaSync(t, alice, room.RoomID, joinEvent.EventID(), syncToken)

			// hs1 should now be tracking @elsie's device list. Enforce this in two steps:
			// 1) Have Alice request Elsie's keys via the CS API and check
			// that hs1 makes a federation request to serve Alice's request.
			// 2) Repeat Alice's request and check that hs1 does _not_ make a
			// second federation request. This proves that hs1 has cached the
			// response from the first step.
			syncToken = mustSyncUntilDeviceListsHas(t, alice, syncToken, "changed", server.UserID("elsie"))
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))
			mustQueryKeysWithoutFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))

			// @elsie updates their device list.
			// @t32alice:hs1 ought to be notified.
			sendDeviceListUpdate("elsie")
			mustSyncUntilDeviceListsHas(t, alice, syncToken, "changed", server.UserID("elsie"))
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))
			// Again, hs1 should have cached @elsie's device list.
			// hs1 should not require a second federation request if Alice rerequests @elsie's keys.
			mustQueryKeysWithoutFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))

			// Finish the partial state join.
			psjResult.FinishStateRequest()
			awaitPartialStateJoinCompletion(t, room, alice)

			// @elsie's device list ought to still be cached.
			mustQueryKeysWithoutFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))
		})

		// test that device lists stop being tracked when a user leaves before the partial state
		// join completes.
		// Similar to the previous test, except @elsie leaves before the partial state join
		// completes.
		t.Run("Device list no longer tracked when new member leaves partial state room", func(t *testing.T) {
			alice, server, userDevicesChannel, room, _, cleanup := setupDeviceListCachingTest(t, deployment, "t33alice")
			defer cleanup()

			// The room starts with @charlie and @derek in it.

			// @t33alice:hs1 joins the room.
			psjResult := beginPartialStateJoin(t, server, room, alice)
			defer psjResult.Destroy(t)

			syncToken := getSyncToken(t, alice)

			// @elsie joins the room.
			joinEvent := createJoinEvent(t, server, room, server.UserID("elsie"))
			room.AddEvent(joinEvent)
			server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{joinEvent.JSON()}, nil)
			awaitEventViaSync(t, alice, room.RoomID, joinEvent.EventID(), syncToken)

			// hs1 should now be tracking @elsie's device list. Enforce this in two steps:
			// 1) Have Alice request Elsie's keys via the CS API and check
			// that hs1 makes a federation request to serve Alice's request.
			// 2) Repeat Alice's request and check that hs1 does _not_ make a
			// second federation request. This proves that hs1 has cached the
			// response from the first step.
			syncToken = mustSyncUntilDeviceListsHas(t, alice, syncToken, "changed", server.UserID("elsie"))
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))
			mustQueryKeysWithoutFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))

			// @elsie leaves the room.
			leaveEvent := createLeaveEvent(t, server, room, server.UserID("elsie"))
			room.AddEvent(leaveEvent)
			server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{leaveEvent.JSON()}, nil)
			awaitEventViaSync(t, alice, room.RoomID, leaveEvent.EventID(), syncToken)

			// hs1 should no longer be tracking elsie's device list; subsequent
			// key requests from alice require a federation request.
			mustSyncUntilDeviceListsHas(t, alice, syncToken, "left", server.UserID("elsie"))
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))
		})

		// test that device lists stop being tracked when leaving a partial state room before the
		// partial state join completes.
		t.Run("Device list no longer tracked when leaving partial state room", func(t *testing.T) {
			// Skipped until https://github.com/matrix-org/synapse/issues/12802 has been addressed.
			t.Skip("Cannot yet leave a room during resync")

			alice, server, userDevicesChannel, room, _, cleanup := setupDeviceListCachingTest(t, deployment, "t34alice")
			defer cleanup()

			// The room starts with @charlie and @derek in it.

			// @t34alice:hs1 joins the room.
			psjResult := beginPartialStateJoin(t, server, room, alice)
			defer psjResult.Destroy(t)

			syncToken := getSyncToken(t, alice)

			// @elsie joins the room.
			joinEvent := createJoinEvent(t, server, room, server.UserID("elsie"))
			room.AddEvent(joinEvent)
			server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{joinEvent.JSON()}, nil)
			awaitEventViaSync(t, alice, room.RoomID, joinEvent.EventID(), syncToken)

			// hs1 should now be tracking @elsie's device list. Enforce this in two steps:
			// 1) Have Alice request Elsie's keys via the CS API and check
			// that hs1 makes a federation request to serve Alice's request.
			// 2) Repeat Alice's request and check that hs1 does _not_ make a
			// second federation request. This proves that hs1 has cached the
			// response from the first step.
			syncToken = mustSyncUntilDeviceListsHas(t, alice, syncToken, "changed", server.UserID("elsie"))
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))
			mustQueryKeysWithoutFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))

			// alice aborts her join before the resync completes
			alice.LeaveRoom(t, room.RoomID)

			// hs1 should no longer be tracking elsie's device list; subsequent
			// key requests from alice require a federation request.
			mustSyncUntilDeviceListsHas(t, alice, syncToken, "left", server.UserID("elsie"))
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))
		})

		// test that device lists stop being tracked when leaving a partial state room due to
		// failure to complete the partial state join.
		t.Run("Device list no longer tracked when failing to complete partial state join", func(t *testing.T) {
			// Skipped until https://github.com/matrix-org/synapse/issues/13000 has been addressed.
			t.Skip("Cannot yet abort a partial state join")

			alice, server, userDevicesChannel, room, _, cleanup := setupDeviceListCachingTest(t, deployment, "t35alice")
			defer cleanup()

			// The room starts with @charlie and @derek in it.

			// @t35alice:hs1 joins the room.
			psjResult := beginPartialStateJoin(t, server, room, alice)
			defer psjResult.Destroy(t)

			syncToken := getSyncToken(t, alice)

			// @elsie joins the room.
			joinEvent := createJoinEvent(t, server, room, server.UserID("elsie"))
			room.AddEvent(joinEvent)
			server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{joinEvent.JSON()}, nil)
			awaitEventViaSync(t, alice, room.RoomID, joinEvent.EventID(), "")

			// hs1 should now be tracking @elsie's device list. Enforce this in two steps:
			// 1) Have Alice request Elsie's keys via the CS API and check
			// that hs1 makes a federation request to serve Alice's request.
			// 2) Repeat Alice's request and check that hs1 does _not_ make a
			// second federation request. This proves that hs1 has cached the
			// response from the first step.
			syncToken = mustSyncUntilDeviceListsHas(t, alice, syncToken, "changed", server.UserID("elsie"))
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))
			mustQueryKeysWithoutFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))

			t.Fatalf("TODO: fail the partial state join")
			psjResult.FinishStateRequest()
			awaitPartialStateJoinCompletion(t, room, alice)

			// hs1 should no longer be tracking elsie's device list; subsequent
			// key requests from alice require a federation request.
			mustSyncUntilDeviceListsHas(t, alice, syncToken, "left", server.UserID("elsie"))
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))
		})

		// setupUserIncorrectlyInRoom tricks the homeserver under test into thinking that @elsie is
		// in the room when they have really been kicked. Once the partial state join completes,
		// @elsie will be discovered to be no longer in the room.
		setupUserIncorrectlyInRoom := func(
			t *testing.T, deployment *docker.Deployment, alice *client.CSAPI,
			server *federation.Server, room *federation.ServerRoom,
		) (syncToken string, psjResult partialStateJoinResult) {
			charlie := server.UserID("charlie")
			derek := server.UserID("derek")
			elsie := server.UserID("elsie")
			fred := server.UserID("fred")

			// The room starts with @charlie and @derek in it.
			// @charlie makes @fred an admin.
			// @charlie makes @derek a moderator.
			var powerLevelsContent map[string]interface{}
			json.Unmarshal(room.CurrentState("m.room.power_levels", "").Content(), &powerLevelsContent)
			powerLevelsContent["users"].(map[string]interface{})[derek] = 50
			powerLevelsContent["users"].(map[string]interface{})[fred] = 100
			room.AddEvent(server.MustCreateEvent(t, room, b.Event{
				Type:     "m.room.power_levels",
				StateKey: b.Ptr(""),
				Sender:   charlie,
				Content:  powerLevelsContent,
			}))

			// @fred joins and leaves the room.
			fredJoinEvent := createJoinEvent(t, server, room, fred)
			room.AddEvent(fredJoinEvent)
			fredLeaveEvent := createLeaveEvent(t, server, room, fred)
			room.AddEvent(fredLeaveEvent)

			// @alice:hs1 joins the room.
			psjResult = beginPartialStateJoin(t, server, room, alice)

			// @elsie joins the room.
			joinEvent := createJoinEvent(t, server, room, elsie)
			room.AddEvent(joinEvent)
			server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{joinEvent.JSON()}, nil)
			syncToken = awaitEventViaSync(t, alice, room.RoomID, joinEvent.EventID(), "")

			// @fred "bans" @derek.
			// This is incorrectly accepted, since the homeserver under test does not know whether
			// @fred is really in the room.
			// This event has to be a ban, rather than a kick, otherwise state resolution can bring
			// @derek back into the room and ruin the test setup.
			badKickEvent := server.MustCreateEvent(t, room, b.Event{
				Type:     "m.room.member",
				StateKey: b.Ptr(derek),
				Sender:   fred,
				Content:  map[string]interface{}{"membership": "ban"},
				AuthEvents: room.EventIDsOrReferences([]*gomatrixserverlib.Event{
					room.CurrentState("m.room.create", ""),
					room.CurrentState("m.room.power_levels", ""),
					fredJoinEvent,
				}),
			})
			room.Timeline = append(room.Timeline, badKickEvent)
			room.Depth = badKickEvent.Depth()
			room.ForwardExtremities = []string{badKickEvent.EventID()}
			server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{badKickEvent.JSON()}, nil)
			syncToken = awaitEventViaSync(t, alice, room.RoomID, badKickEvent.EventID(), syncToken)

			// @derek kicks @elsie.
			// This is incorrectly rejected since the homeserver under test incorrectly thinks
			// @derek had been kicked from the room.
			kickEvent := server.MustCreateEvent(t, room, b.Event{
				Type:     "m.room.member",
				StateKey: b.Ptr(elsie),
				Sender:   derek,
				Content:  map[string]interface{}{"membership": "leave"},
			})
			room.AddEvent(kickEvent)
			server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{kickEvent.JSON()}, nil)

			// Ensure that the kick event has been persisted.
			sentinelEvent := psjResult.CreateMessageEvent(t, "charlie", nil)
			room.AddEvent(sentinelEvent)
			server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{sentinelEvent.JSON()}, nil)
			syncToken = awaitEventViaSync(t, alice, room.RoomID, sentinelEvent.EventID(), syncToken)

			// Check that the last kick was incorrectly rejected.
			must.MatchResponse(t,
				alice.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", room.RoomID, "event", kickEvent.EventID()}),
				match.HTTPResponse{
					StatusCode: 404,
					JSON: []match.JSON{
						match.JSONKeyEqual("errcode", "M_NOT_FOUND"),
					},
				},
			)

			return syncToken, psjResult
		}

		// test that device lists stop being tracked when it is discovered that a remote user is not
		// in a room once a partial state join completes.
		t.Run("Device list no longer tracked for user incorrectly believed to be in room", func(t *testing.T) {
			alice, server, userDevicesChannel, room, _, cleanup := setupDeviceListCachingTest(t, deployment, "t36alice")
			defer cleanup()

			// The room starts with @charlie and @derek in it.
			// @charlie leaves the room.
			// @t36alice:hs1 joins the room.
			// @elsie joins the room.
			// @charlie "kicks" @derek, which the homeserver under test incorrectly accepts.
			// @derek kicks @elsie, which the homeserver under test incorrectly rejects.
			_, psjResult := setupUserIncorrectlyInRoom(t, deployment, alice, server, room)
			defer psjResult.Destroy(t)
			// @elsie is now incorrectly believed to be in the room.

			// The homeserver under test incorrectly thinks it is subscribed to @elsie's device list updates.
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))
			mustQueryKeysWithoutFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))

			// Finish the partial state join.
			// The homeserver under test will discover that @elsie was actually not in the room.
			psjResult.FinishStateRequest()
			awaitPartialStateJoinCompletion(t, room, alice)

			// @elsie's device list ought to no longer be cached.
			// `device_lists.left` is not working yet: https://github.com/matrix-org/synapse/issues/13886
			// mustSyncUntilDeviceListsHas(t, alice, syncToken, "left", server.UserID("elsie"))
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))
		})

		// test that cached device lists are flushed when it is discovered that a remote user was
		// not in a room the whole time once a partial state join completes.
		t.Run("Device list tracking for user incorrectly believed to be in room when they rejoin before partial state join completes", func(t *testing.T) {
			// Tracked in https://github.com/matrix-org/synapse/issues/13887.
			t.Skip("This edge case is being ignored for now.")

			alice, server, userDevicesChannel, room, _, cleanup := setupDeviceListCachingTest(t, deployment, "t37alice")
			defer cleanup()

			// The room starts with @charlie and @derek in it.
			// @charlie leaves the room.
			// @t37alice:hs1 joins the room.
			// @elsie joins the room.
			// @charlie "kicks" @derek, which the homeserver under test incorrectly accepts.
			// @derek kicks @elsie, which the homeserver under test incorrectly rejects.
			syncToken, psjResult := setupUserIncorrectlyInRoom(t, deployment, alice, server, room)
			defer psjResult.Destroy(t)
			// @elsie is now incorrectly believed to be in the room.

			// The homeserver under test incorrectly thinks it is subscribed to @elsie's device list updates.
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))
			mustQueryKeysWithoutFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))

			// @elsie rejoins the room.
			joinEvent := createJoinEvent(t, server, room, server.UserID("elsie"))
			room.AddEvent(joinEvent)
			server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{joinEvent.JSON()}, nil)
			awaitEventViaSync(t, alice, room.RoomID, joinEvent.EventID(), syncToken)

			// @elsie's device list is still cached.
			mustQueryKeysWithoutFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))

			// Finish the partial state join.
			// The homeserver under test will discover that there was a period where @elsie was
			// actually not in the room.
			psjResult.FinishStateRequest()
			awaitPartialStateJoinCompletion(t, room, alice)

			// @elsie's device list ought to have been flushed from the cache.
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))
		})

		// test that device lists stop being tracked when it is discovered that a remote user is not
		// in a room once a partial state join completes.
		// Similar to a previous test, except @elsie rejoins the room after the partial state join
		// completes, so that their device list is being tracked again at the time we test the
		// device list cache.
		t.Run("Device list tracking for user incorrectly believed to be in room when they rejoin after partial state join completes", func(t *testing.T) {
			alice, server, userDevicesChannel, room, _, cleanup := setupDeviceListCachingTest(t, deployment, "t38alice")
			defer cleanup()

			// The room starts with @charlie and @derek in it.
			// @charlie leaves the room.
			// @t38alice:hs1 joins the room.
			// @elsie joins the room.
			// @charlie "kicks" @derek, which the homeserver under test incorrectly accepts.
			// @derek kicks @elsie, which the homeserver under test incorrectly rejects.
			syncToken, psjResult := setupUserIncorrectlyInRoom(t, deployment, alice, server, room)
			defer psjResult.Destroy(t)
			// @elsie is now incorrectly believed to be in the room.

			// The homeserver under test incorrectly thinks it is subscribed to @elsie's device list updates.
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))
			mustQueryKeysWithoutFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))

			// Finish the partial state join.
			// The homeserver under test will discover that @elsie was actually not in the room.
			psjResult.FinishStateRequest()
			awaitPartialStateJoinCompletion(t, room, alice)
			// `device_lists.left` is not working yet: https://github.com/matrix-org/synapse/issues/13886
			// mustSyncUntilDeviceListsHas(t, alice, syncToken, "left", server.UserID("elsie"))

			// @elsie rejoins the room.
			joinEvent := createJoinEvent(t, server, room, server.UserID("elsie"))
			room.AddEvent(joinEvent)
			server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{joinEvent.JSON()}, nil)
			awaitEventViaSync(t, alice, room.RoomID, joinEvent.EventID(), syncToken)

			// @elsie's device list ought to have been flushed from the cache.
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))
		})

		// test that cached device lists are flushed when it is discovered that a remote user did
		// not share a room the whole time once a partial state join completes.
		t.Run("Device list tracking for user incorrectly believed to be in room when they join another shared room before partial state join completes", func(t *testing.T) {
			// Tracked in https://github.com/matrix-org/synapse/issues/13887.
			t.Skip("This edge case is being ignored for now.")

			alice, server, userDevicesChannel, room, _, cleanup := setupDeviceListCachingTest(t, deployment, "t39alice")
			defer cleanup()

			// The room starts with @charlie and @derek in it.
			// @charlie leaves the room.
			// @t39alice:hs1 joins the room.
			// @elsie joins the room.
			// @charlie "kicks" @derek, which the homeserver under test incorrectly accepts.
			// @derek kicks @elsie, which the homeserver under test incorrectly rejects.
			syncToken, psjResult := setupUserIncorrectlyInRoom(t, deployment, alice, server, room)
			defer psjResult.Destroy(t)
			// @elsie is now incorrectly believed to be in the room.

			// The homeserver under test incorrectly thinks it is subscribed to @elsie's device list updates.
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))
			mustQueryKeysWithoutFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))

			// @t39alice:hs1 creates a public room.
			otherRoomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})

			// @elsie joins the room.
			// The homeserver under test is now subscribed to @elsie's device list updates.
			server.MustJoinRoom(t, deployment, "hs1", otherRoomID, server.UserID("elsie"))
			alice.MustSyncUntil(t,
				client.SyncReq{
					Since:  syncToken,
					Filter: buildLazyLoadingSyncFilter(nil),
				},
				client.SyncJoinedTo(server.UserID("elsie"), otherRoomID),
			)

			// The cache device list for @elsie is stale, but the homeserver does not know that yet.
			mustQueryKeysWithoutFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))

			// Finish the partial state join.
			// The homeserver under test will discover that @elsie was actually not in the room, and
			// so did not share a room the whole time.
			psjResult.FinishStateRequest()
			awaitPartialStateJoinCompletion(t, room, alice)

			// @elsie's device list ought to be evicted from the cache.
			mustSyncUntilDeviceListsHas(t, alice, syncToken, "changed", server.UserID("elsie"))
			mustQueryKeysWithFederationRequest(t, alice, userDevicesChannel, server.UserID("elsie"))
		})
	})

	// Test that a) you can add a room alias during a resync and that
	// b) querying that alias returns at least the servers we were told
	// about in the /send_join response.
	t.Run("Room aliases can be added and queried during a resync", func(t *testing.T) {
		// Alice begins a partial join to a room.
		alice := deployment.RegisterUser(t, "hs1", "t40alice", "secret", false)
		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()

		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		// Alice creates an alias for the room
		aliasName := "#t40alice-room:hs1"
		alice.MustDoFunc(
			t,
			"PUT",
			[]string{"_matrix", "client", "v3", "directory", "room", aliasName},
			client.WithJSONBody(t, map[string]interface{}{
				"room_id": serverRoom.RoomID,
			}),
		)

		// Alice then queries that alias
		response := alice.MustDoFunc(
			t,
			"GET",
			[]string{"_matrix", "client", "v3", "directory", "room", aliasName},
			client.WithJSONBody(t, map[string]interface{}{
				"room_id": serverRoom.RoomID,
			}),
		)

		// The response should be 200 OK, should include the room id and
		// should include both HSes.
		spec := match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyEqual("room_id", serverRoom.RoomID),
				match.JSONCheckOff(
					"servers",
					[]interface{}{"hs1", server.ServerName()},
					func(r gjson.Result) interface{} { return r.Str },
					nil,
				),
			},
		}
		must.MatchResponse(t, response, spec)
	})

	// Test that you can delete a room alias during a resync that you added during
	// the resync.
	t.Run("Room aliases can be added and deleted during a resync", func(t *testing.T) {
		// Alice begins a partial join to a room.
		alice := deployment.RegisterUser(t, "hs1", "t41alice", "secret", false)
		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()

		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		// Alice creates an alias for the room
		aliasName := "#t41alice-room:hs1"
		alice.MustDoFunc(
			t,
			"PUT",
			[]string{"_matrix", "client", "v3", "directory", "room", aliasName},
			client.WithJSONBody(t, map[string]interface{}{
				"room_id": serverRoom.RoomID,
			}),
		)

		// Alice then deletes that alias
		response := alice.MustDoFunc(
			t,
			"DELETE",
			[]string{"_matrix", "client", "v3", "directory", "room", aliasName},
		)

		// The response should be 200 OK. (Strictly speaking it should have an
		// empty json object as the response body but that's not important here)
		spec := match.HTTPResponse{
			StatusCode: 200,
		}
		must.MatchResponse(t, response, spec)
	})

	t.Run("Leaving during resync is seen after the resync", func(t *testing.T) {
		// Before testing that leaves during resyncs are seen during resyncs, sanity
		// check that leaves during resyncs appear after the resync.
		t.Log("Alice begins a partial join to a room")
		alice := deployment.RegisterUser(t, "hs1", "t42alice", "secret", false)
		handleTransactions := federation.HandleTransactionRequests(
			// Accept all PDUs and EDUs
			func(e *gomatrixserverlib.Event) {},
			func(e gomatrixserverlib.EDU) {},
		)
		server := createTestServer(t, deployment, handleTransactions)
		cancel := server.Listen()
		defer cancel()

		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		t.Log("Alice partial-joins her room")
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		t.Log("Alice waits to see her join")
		aliceNextBatch := alice.MustSyncUntil(
			t,
			client.SyncReq{Filter: buildLazyLoadingSyncFilter(nil)},
			client.SyncJoinedTo(alice.UserID, serverRoom.RoomID),
		)

		leaveCompleted := NewWaiter()
		t.Log("Alice starts a leave request")
		go func() {
			alice.LeaveRoom(t, serverRoom.RoomID)
			t.Log("Alice's leave request completed")
			leaveCompleted.Finish()
		}()

		// We want Synapse to receive the leave before its resync completes.
		// HACK: Use a sleep to try and ensure this.
		time.Sleep(250 * time.Millisecond)
		t.Log("The resync finishes")
		psjResult.FinishStateRequest()

		// Now that we've resynced, the leave call should be unblocked.
		leaveCompleted.Wait(t, 1*time.Second)

		t.Log("Alice waits to see her leave appear down /sync")
		aliceNextBatch = alice.MustSyncUntil(
			t,
			client.SyncReq{Since: aliceNextBatch, Filter: buildLazyLoadingSyncFilter(nil)},
			client.SyncLeftFrom(alice.UserID, serverRoom.RoomID),
		)
	})

	t.Run("Leaving a room immediately after joining does not wait for resync", func(t *testing.T) {
		t.Skip("Not yet implemented (synapse#12802)")
		// Prepare to listen for leave events from the HS under test.
		// We're only expecting one leave event, but give the channel extra capacity
		// to avoid deadlock if the HS does something silly.
		leavesChannel := make(chan *gomatrixserverlib.Event, 10)
		handleTransactions := federation.HandleTransactionRequests(
			func(e *gomatrixserverlib.Event) {
				if e.Type() == "m.room.member" {
					if ok := gjson.ValidBytes(e.Content()); !ok {
						t.Fatalf("Received event %s with invalid content: %v", e.EventID(), e.Content())
					}
					content := gjson.ParseBytes(e.Content())
					membership := content.Get("membership")
					if membership.Exists() && membership.Str == "leave" {
						leavesChannel <- e
					}
				}
			},
			// we don't care about EDUs
			func(e gomatrixserverlib.EDU) {},
		)

		t.Log("Alice begins a partial join to a room")
		alice := deployment.RegisterUser(t, "hs1", "t43alice", "secret", false)
		server := createTestServer(
			t,
			deployment,
			handleTransactions,
		)
		cancel := server.Listen()
		defer cancel()

		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy(t)

		t.Log("Alice waits to see her join")
		aliceNextBatch := alice.MustSyncUntil(
			t,
			client.SyncReq{Filter: buildLazyLoadingSyncFilter(nil)},
			client.SyncJoinedTo(alice.UserID, serverRoom.RoomID),
		)

		t.Log("Alice leaves and waits for confirmation")
		alice.LeaveRoom(t, serverRoom.RoomID)
		aliceNextBatch = alice.MustSyncUntil(
			t,
			client.SyncReq{Since: aliceNextBatch, Filter: buildLazyLoadingSyncFilter(nil)},
			client.SyncLeftFrom(alice.UserID, serverRoom.RoomID),
		)

		t.Logf("Alice's leave is recieved by the resident server")
		select {
		case <-time.After(1 * time.Second):
			t.Fatal("Resident server did not receive Alice's leave")
		case e := <-leavesChannel:
			if e.Sender() != alice.UserID {
				t.Errorf("Unexpected leave event %s for %s", e.EventID(), e.Sender())
			}
		}
	})

	t.Run("Room stats are correctly updated once state re-sync completes", func(t *testing.T) {
		// create a user with admin powers as we will need this power to make the remote room visible in the
		// local room list
		terry := deployment.RegisterUser(t, "hs1", "terry", "pass", true)

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, terry.GetDefaultRoomVersion(t))

		// start a partial state join
		psjResult := beginPartialStateJoin(t, server, serverRoom, terry)
		defer psjResult.Destroy(t)

		// make the remote room visible in the local room list
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"visibility": "public",
		})
		terry.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "directory", "list", "room", serverRoom.RoomID}, reqBody)

		// sanity check - before the state has completed syncing state we would expect only one user
		// to show up in the room list
		res := terry.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "publicRooms"})

		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyEqual("chunk.0.num_joined_members", 1),
			}})

		// finish syncing the state
		psjResult.FinishStateRequest()
		awaitPartialStateJoinCompletion(t, psjResult.ServerRoom, terry)

		// In Synapse rooms stats are updated by a background job which is not guaranteed to have completed by the time
		// the state sync has completed. To account for that, we check for up to 3 seconds that the job has completed.
		// The number of joined users should now be 3: one local user (terry) and two remote (charlie and derek)
		terry.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "publicRooms"},
			client.WithRetryUntil(time.Second*3, func(res *http.Response) bool {
				body, err := ioutil.ReadAll(res.Body)
				if err != nil {
					t.Fatalf("something broke: %v", err)
				}
				numJoinedMembers := gjson.GetBytes(body, "chunk.0.num_joined_members")
				if numJoinedMembers.Int() == 3 {
					return true
				}
				return false
			}))
		})

	// TODO: tests which assert that:
	//   - Join+Join+Leave+Leave works
	//   - Join+Leave+Join works
	//   - Join+Leave+Rejoin works
	//   - Join + remote kick works
	//   - Join + remote ban works, then cannot rejoin
}

// test reception of an event over federation during a resync
// sends the given event to the homeserver under test, checks that a client can see it and checks
// the state at the event. returns the new sync token after the event.
func testReceiveEventDuringPartialStateJoin(
	t *testing.T, deployment *docker.Deployment, alice *client.CSAPI, psjResult partialStateJoinResult, event *gomatrixserverlib.Event, syncToken string,
) string {
	// send the event to the homeserver
	psjResult.Server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{event.JSON()}, nil)

	syncToken = awaitEventViaSync(t, alice, psjResult.ServerRoom.RoomID, event.EventID(), syncToken)

	// fire off a /state_ids request for the last event.
	// it must either:
	//   * block because the homeserver does not have full state at the last event
	//   * or 403 because the homeserver does not have full state yet and does not consider the
	//     Complement homeserver to be in the room
	// Synapse's behaviour will likely change once https://github.com/matrix-org/synapse/issues/13288
	// is resolved. For now, we use this to check whether Synapse has calculated the partial state
	// flag for the last event correctly.

	stateReq := gomatrixserverlib.NewFederationRequest("GET", gomatrixserverlib.ServerName(psjResult.Server.ServerName()), "hs1",
		fmt.Sprintf("/_matrix/federation/v1/state_ids/%s?event_id=%s",
			url.PathEscape(psjResult.ServerRoom.RoomID),
			url.QueryEscape(event.EventID()),
		),
	)
	var respStateIDs gomatrixserverlib.RespStateIDs
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := psjResult.Server.SendFederationRequest(ctx, t, deployment, stateReq, &respStateIDs)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			t.Logf("/state_ids request for event %s blocked as expected", event.EventID())
		} else if httpErr, ok := err.(gomatrix.HTTPError); ok && httpErr.Code == 403 {
			t.Logf("/state_ids request for event %s returned 403 as expected", event.EventID())
		} else {
			t.Errorf("/state_ids request returned non-200: %s", err)
		}
	} else {
		// since we have not yet given the homeserver the full state at the join event and allowed
		// the partial join to complete, it can't possibly know the full state at the last event.
		// While it may be possible for the response to be correct by some accident of state res,
		// the homeserver is still wrong in spirit.
		t.Fatalf("/state_ids request for event %s did not block when it should have", event.EventID())
	}

	// allow the partial join to complete
	psjResult.FinishStateRequest()
	alice.MustSyncUntil(t,
		client.SyncReq{},
		client.SyncJoinedTo(alice.UserID, psjResult.ServerRoom.RoomID),
	)

	// FIXME: if we try to do a /state_ids request immediately, it will race against update of the "current state", and
	//   our request may be rejected due to https://github.com/matrix-org/synapse/issues/13288.
	//   By way of a workaround, request a remote user's current membership, which should block until the current state
	//   is updated.
	alice.DoFunc(
		t,
		"GET",
		[]string{"_matrix", "client", "v3", "rooms", psjResult.ServerRoom.RoomID, "state", "m.room.member", "@non-existent:remote"},
	)

	// check the server's idea of the state at the event. We do this by making a `state_ids` request over federation
	stateReq = gomatrixserverlib.NewFederationRequest("GET", gomatrixserverlib.ServerName(psjResult.Server.ServerName()), "hs1",
		fmt.Sprintf("/_matrix/federation/v1/state_ids/%s?event_id=%s",
			url.PathEscape(psjResult.ServerRoom.RoomID),
			url.QueryEscape(event.EventID()),
		),
	)
	if err := psjResult.Server.SendFederationRequest(context.Background(), t, deployment, stateReq, &respStateIDs); err != nil {
		t.Errorf("/state_ids request returned non-200: %s", err)
		return syncToken
	}
	var gotState, expectedState []interface{}
	for _, ev := range respStateIDs.StateEventIDs {
		gotState = append(gotState, ev)
	}
	for _, ev := range psjResult.ServerRoom.AllCurrentState() {
		expectedState = append(expectedState, ev.EventID())
	}
	must.CheckOffAll(t, gotState, expectedState)

	return syncToken
}

// awaitEventViaSync waits for alice to be able to see a given event via an incremental lazy-loading
// /sync and returns the new sync token after
func awaitEventViaSync(t *testing.T, alice *client.CSAPI, roomID string, eventID string, syncToken string) string {
	t.Helper()

	// check that a lazy-loading sync can see the event
	syncToken = alice.MustSyncUntil(t,
		client.SyncReq{
			Since:  syncToken,
			Filter: buildLazyLoadingSyncFilter(nil),
		},
		client.SyncTimelineHasEventID(roomID, eventID),
	)

	t.Logf("Alice successfully received event %s via /sync", eventID)

	return syncToken
}

// awaitEventArrival waits for alice to be able to see a given event via /event
func awaitEventArrival(t *testing.T, timeout time.Duration, alice *client.CSAPI, roomID string, eventID string) {
	t.Helper()

	// Alice should be able to see the event with an /event request. We might have to try it a few times.
	alice.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "event", eventID},
		client.WithRetryUntil(timeout, func(res *http.Response) bool {
			if res.StatusCode == 200 {
				return true
			}
			eventResBody := client.ParseJSON(t, res)
			if res.StatusCode == 404 && gjson.GetBytes(eventResBody, "errcode").String() == "M_NOT_FOUND" {
				return false
			}
			t.Fatalf("GET /event failed with %d: %s", res.StatusCode, string(eventResBody))
			return false
		}),
	)
	t.Logf("Alice successfully observed event %s via /event", eventID)
}

// awaitPartialStateJoinCompletion waits until the joined room is no longer partial-stated
func awaitPartialStateJoinCompletion(
	t *testing.T, room *federation.ServerRoom, user *client.CSAPI,
) {
	t.Helper()

	// Use a `/members` request to wait for the room to be un-partial stated.
	// We avoid using `/sync`, as it only waits (or used to wait) for full state at
	// particular events, rather than the whole room.
	user.MustDoFunc(
		t,
		"GET",
		[]string{"_matrix", "client", "v3", "rooms", room.RoomID, "members"},
	)
	t.Logf("%s's partial state join to %s completed.", user.UserID, room.RoomID)
}

// buildLazyLoadingSyncFilter constructs a json-marshalled filter suitable the 'Filter' field of a client.SyncReq
func buildLazyLoadingSyncFilter(timelineOptions map[string]interface{}) string {
	timelineFilter := map[string]interface{}{
		"lazy_load_members": true,
	}

	for k, v := range timelineOptions {
		timelineFilter[k] = v
	}

	j, _ := json.Marshal(map[string]interface{}{
		"room": map[string]interface{}{
			"timeline": timelineFilter,
			"state": map[string]interface{}{
				"lazy_load_members": true,
			},
		},
	})
	return string(j)
}

// partialStateJoinResult is the result of beginPartialStateJoin
type partialStateJoinResult struct {
	Server                           *federation.Server
	ServerRoom                       *federation.ServerRoom
	User                             *client.CSAPI
	fedStateIdsRequestReceivedWaiter *Waiter
	fedStateIdsSendResponseWaiter    *Waiter
}

// beginPartialStateJoin has a test user attempt to join the given room.
//
// It returns a partialStateJoinResult, which must be Destroy'd on completion.
//
// When this method completes, the /join request will have completed, but the
// state has not yet been re-synced. To allow the re-sync to proceed, call
// partialStateJoinResult.FinishStateRequest.
func beginPartialStateJoin(t *testing.T, server *federation.Server, serverRoom *federation.ServerRoom, joiningUser *client.CSAPI) partialStateJoinResult {
	// we store the Server and ServerRoom for the benefit of utilities like testReceiveEventDuringPartialStateJoin
	result := partialStateJoinResult{
		Server:     server,
		ServerRoom: serverRoom,
		User:       joiningUser,
	}
	success := false
	defer func() {
		if !success {
			result.Destroy(t)
		}
	}()

	// some things for orchestration
	result.fedStateIdsRequestReceivedWaiter = NewWaiter()
	result.fedStateIdsSendResponseWaiter = NewWaiter()

	// register a handler for /state_ids requests for the most recent event,
	// which finishes fedStateIdsRequestReceivedWaiter, then
	// waits for fedStateIdsSendResponseWaiter and sends a reply
	lastEvent := serverRoom.Timeline[len(serverRoom.Timeline)-1]
	currentState := serverRoom.AllCurrentState()
	handleStateIdsRequests(
		t, server, serverRoom,
		lastEvent.EventID(), currentState,
		result.fedStateIdsRequestReceivedWaiter, result.fedStateIdsSendResponseWaiter,
	)

	// a handler for /state requests, which sends a sensible response
	handleStateRequests(
		t, server, serverRoom,
		lastEvent.EventID(), currentState,
		nil, nil,
	)

	// have joiningUser join the room by room ID.
	joiningUser.JoinRoom(t, serverRoom.RoomID, []string{server.ServerName()})
	t.Logf("/join request completed")

	success = true
	return result
}

// Destroy cleans up the resources associated with the join attempt. It must
// be called once the test is finished
func (psj *partialStateJoinResult) Destroy(t *testing.T) {
	if psj.fedStateIdsSendResponseWaiter != nil {
		psj.fedStateIdsSendResponseWaiter.Finish()
	}

	if psj.fedStateIdsRequestReceivedWaiter != nil {
		psj.fedStateIdsRequestReceivedWaiter.Finish()
	}

	// Since the same deployment is being used across multiple tests, ensure that it
	// has finished all federation activity before tearing down the Complement server.
	// Otherwise the homeserver at the Complement's hostname:port combination may be
	// considered offline and interfere with subsequent tests.
	awaitPartialStateJoinCompletion(t, psj.ServerRoom, psj.User)
}

// send a message into the room without letting the homeserver under test know about it.
func (psj *partialStateJoinResult) CreateMessageEvent(t *testing.T, senderLocalpart string, prevEventIDs []string) *gomatrixserverlib.Event {
	var prevEvents interface{}
	if prevEventIDs == nil {
		prevEvents = nil
	} else {
		prevEvents = prevEventIDs
	}

	event := psj.Server.MustCreateEvent(t, psj.ServerRoom, b.Event{
		Type:   "m.room.message",
		Sender: psj.Server.UserID(senderLocalpart),
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message",
		},
		PrevEvents: prevEvents,
	})
	psj.ServerRoom.AddEvent(event)
	return event
}

// wait for a /state_ids request for the test room to arrive
func (psj *partialStateJoinResult) AwaitStateIdsRequest(t *testing.T) {
	psj.fedStateIdsRequestReceivedWaiter.Waitf(t, 5*time.Second, "Waiting for /state_ids request")
}

// allow the /state_ids request to complete, thus allowing the state re-sync to complete
func (psj *partialStateJoinResult) FinishStateRequest() {
	psj.fedStateIdsSendResponseWaiter.Finish()
}

// handleStateIdsRequests registers a handler for /state_ids requests for 'eventID'
//
// the returned state is as passed in 'roomState'
//
// if requestReceivedWaiter is not nil, it will be Finish()ed when the request arrives.
// if sendResponseWaiter is not nil, we will Wait() for it to finish before sending the response.
func handleStateIdsRequests(
	t *testing.T, srv *federation.Server, serverRoom *federation.ServerRoom,
	eventID string, roomState []*gomatrixserverlib.Event,
	requestReceivedWaiter *Waiter, sendResponseWaiter *Waiter,
) {
	srv.Mux().NewRoute().Methods("GET").Path(
		fmt.Sprintf("/_matrix/federation/v1/state_ids/%s", serverRoom.RoomID),
	).Queries("event_id", eventID).Handler(
		http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			queryParams := req.URL.Query()
			t.Logf("Incoming state_ids request for event %s in room %s", queryParams["event_id"], serverRoom.RoomID)
			if requestReceivedWaiter != nil {
				requestReceivedWaiter.Finish()
			}
			if sendResponseWaiter != nil {
				sendResponseWaiter.Waitf(t, 60*time.Second, "Waiting for /state_ids request")
			}
			t.Logf("Replying to /state_ids request for event %s", queryParams["event_id"])

			res := gomatrixserverlib.RespStateIDs{
				AuthEventIDs:  eventIDsFromEvents(serverRoom.AuthChainForEvents(roomState)),
				StateEventIDs: eventIDsFromEvents(roomState),
			}
			w.WriteHeader(200)
			jsonb, _ := json.Marshal(res)

			if _, err := w.Write(jsonb); err != nil {
				t.Errorf("Error writing to request: %v", err)
			}
		}),
	)
	t.Logf("Registered state_ids handler for event %s", eventID)
}

// makeStateHandler returns a handler for /state requests for 'eventID'
//
// the returned state is as passed in 'roomState'
//
// if requestReceivedWaiter is not nil, it will be Finish()ed when the request arrives.
// if sendResponseWaiter is not nil, we will Wait() for it to finish before sending the response.
func handleStateRequests(
	t *testing.T, srv *federation.Server, serverRoom *federation.ServerRoom,
	eventID string, roomState []*gomatrixserverlib.Event,
	requestReceivedWaiter *Waiter, sendResponseWaiter *Waiter,
) {
	srv.Mux().NewRoute().Methods("GET").Path(
		fmt.Sprintf("/_matrix/federation/v1/state/%s", serverRoom.RoomID),
	).Queries("event_id", eventID).Handler(
		http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			queryParams := req.URL.Query()
			t.Logf("Incoming state request for event %s in room %s", queryParams["event_id"], serverRoom.RoomID)
			if requestReceivedWaiter != nil {
				requestReceivedWaiter.Finish()
			}
			if sendResponseWaiter != nil {
				sendResponseWaiter.Waitf(t, 60*time.Second, "Waiting for /state request")
			}

			t.Logf("Replying to /state request for event %s", queryParams["event_id"])

			res := gomatrixserverlib.RespState{
				AuthEvents:  gomatrixserverlib.NewEventJSONsFromEvents(serverRoom.AuthChainForEvents(roomState)),
				StateEvents: gomatrixserverlib.NewEventJSONsFromEvents(roomState),
			}
			w.WriteHeader(200)
			jsonb, _ := json.Marshal(res)

			if _, err := w.Write(jsonb); err != nil {
				t.Errorf("Error writing to request: %v", err)
			}
		}),
	)
	t.Logf("Registered /state handler for event %s", eventID)
}

// register a handler for `/get_missing_events` requests
//
// This can (currently) only handle a single `/get_missing_events` request, and the "latest_events" in the request
// must match those listed in "expectedLatestEvents" (otherwise the test is failed).
func handleGetMissingEventsRequests(
	t *testing.T, srv *federation.Server, serverRoom *federation.ServerRoom,
	expectedLatestEvents []string, eventsToReturn []*gomatrixserverlib.Event,
) {
	srv.Mux().HandleFunc(fmt.Sprintf("/_matrix/federation/v1/get_missing_events/%s", serverRoom.RoomID), func(w http.ResponseWriter, req *http.Request) {
		body, err := ioutil.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("unable to read /get_missing_events request body: %s", err)
		}
		var getMissingEventsRequest gomatrixserverlib.MissingEvents
		err = json.Unmarshal(body, &getMissingEventsRequest)
		if err != nil {
			t.Fatalf("unable to unmarshall /get_missing_events request body: %s", err)
		}

		t.Logf("Incoming get_missing_events request for prev events of %s in room %s", getMissingEventsRequest.LatestEvents, serverRoom.RoomID)
		if !reflect.DeepEqual(expectedLatestEvents, getMissingEventsRequest.LatestEvents) {
			t.Fatalf("getMissingEventsRequest.LatestEvents: got %v, wanted %v", getMissingEventsRequest, expectedLatestEvents)
		}

		responseBytes, _ := json.Marshal(gomatrixserverlib.RespMissingEvents{
			Events: gomatrixserverlib.NewEventJSONsFromEvents(eventsToReturn),
		})
		w.WriteHeader(200)
		w.Write(responseBytes)
	}).Methods("POST")
}

func eventIDsFromEvents(he []*gomatrixserverlib.Event) []string {
	eventIDs := make([]string, len(he))
	for i := range he {
		eventIDs[i] = he[i].EventID()
	}
	return eventIDs
}
