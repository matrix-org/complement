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
	createTestServer := func(t *testing.T, deployment *docker.Deployment) *federation.Server {
		return federation.NewServer(t, deployment,
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
		)
	}

	// createTestRoom creates a room on the complement server suitable for many of the tests in this file
	createTestRoom := func(t *testing.T, server *federation.Server, roomVer gomatrixserverlib.RoomVersion) *federation.ServerRoom {
		// create the room on the complement server, with charlie and derek as members
		serverRoom := server.MustMakeRoom(t, roomVer, federation.InitialRoomEvents(roomVer, server.UserID("charlie")))
		serverRoom.AddEvent(server.MustCreateEvent(t, serverRoom, b.Event{
			Type:     "m.room.member",
			StateKey: b.Ptr(server.UserID("derek")),
			Sender:   server.UserID("derek"),
			Content: map[string]interface{}{
				"membership": "join",
			},
		}))
		return serverRoom
	}

	// getSyncToken gets the latest sync token
	getSyncToken := func(t *testing.T, alice *client.CSAPI) string {
		_, syncToken := alice.MustSync(t,
			client.SyncReq{
				Filter:        buildLazyLoadingSyncFilter(nil),
				TimeoutMillis: "0",
			},
		)
		return syncToken
	}

	// test that a regular /sync request made during a partial-state /send_join
	// request blocks until the state is correctly synced.
	t.Run("SyncBlocksDuringPartialStateJoin", func(t *testing.T) {
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy()

		// Alice has now joined the room, and the server is syncing the state in the background.

		// attempts to sync should now block. Fire off a goroutine to try it.
		syncResponseChan := make(chan gjson.Result)
		defer close(syncResponseChan)
		go func() {
			response, _ := alice.MustSync(t, client.SyncReq{})
			syncResponseChan <- response
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
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy()

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
		t.Skip("Cannot yet send events during resync")
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy()

		alice.Client.Timeout = 2 * time.Second
		paths := []string{"_matrix", "client", "v3", "rooms", serverRoom.RoomID, "send", "m.room.message", "0"}
		res := alice.MustDoFunc(t, "PUT", paths, client.WithJSONBody(t, map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world!",
		}))
		body := gjson.ParseBytes(client.ParseJSON(t, res))
		eventID := body.Get("event_id").Str
		t.Logf("Alice sent event event ID %s", eventID)
	})

	// we should be able to receive events over federation during the resync
	t.Run("CanReceiveEventsDuringPartialStateJoin", func(t *testing.T) {
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy()

		// the HS will make an /event_auth request for the event
		federation.HandleEventAuthRequests()(server)

		event := psjResult.CreateMessageEvent(t, "derek", nil)
		t.Logf("Derek created event with ID %s", event.EventID())

		// derek sends an event in the room
		testReceiveEventDuringPartialStateJoin(t, deployment, alice, psjResult, event)
	})

	// we should be able to receive events with a missing prev event over federation during the resync
	t.Run("CanReceiveEventsWithMissingParentsDuringPartialStateJoin", func(t *testing.T) {
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy()

		// we construct the following event graph:
		// ... <-- M <-- A <-- B
		//
		// M is @alice:hs1's join event.
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
		testReceiveEventDuringPartialStateJoin(t, deployment, alice, psjResult, eventB)
	})

	// we should be able to receive events with partially missing prev events over federation during the resync
	t.Run("CanReceiveEventsWithHalfMissingParentsDuringPartialStateJoin", func(t *testing.T) {
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy()

		// we construct the following event graph:
		//         +---------+
		//         v          \
		// ... <-- M <-- A <-- B
		//
		// M is @alice:hs1's join event.
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
		testReceiveEventDuringPartialStateJoin(t, deployment, alice, psjResult, eventB)
	})

	// we should be able to receive events with a missing prev event, with half missing prev events,
	// over federation during the resync
	t.Run("CanReceiveEventsWithHalfMissingGrandparentsDuringPartialStateJoin", func(t *testing.T) {
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy()

		// we construct the following event graph:
		//         +---------+
		//         v          \
		// ... <-- M <-- A <-- B <-- C
		//
		// M is @alice:hs1's join event.
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
		testReceiveEventDuringPartialStateJoin(t, deployment, alice, psjResult, eventC)
	})

	// a request to (client-side) /members?at= should block until the (federation) /state request completes
	// TODO(faster_joins): also need to test /state, and /members without an `at`, which follow a different path
	t.Run("MembersRequestBlocksDuringPartialStateJoin", func(t *testing.T) {
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy()

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
		defer close(clientMembersRequestResponseChan)
		go func() {
			queryParams := url.Values{}
			queryParams.Set("at", syncToken)
			clientMembersRequestResponseChan <- alice.MustDoFunc(
				t,
				"GET",
				[]string{"_matrix", "client", "v3", "rooms", serverRoom.RoomID, "members"},
				client.WithQueries(queryParams),
			)
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
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy()

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
		defer close(syncResponseChan)
		go func() {
			response, _ := alice.MustSync(t, client.SyncReq{})
			syncResponseChan <- response
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
		defer close(syncResponseChan)
		go func() {
			response, _ := charlie.MustSync(t, client.SyncReq{})
			syncResponseChan <- response
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
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy()

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
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy()

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
		awaitEventArrival(t, time.Second, alice, serverRoom.RoomID, timelineEvent2.EventID())

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

		// wait for the last outlier to arrive
		awaitEventArrival(t, 10*time.Second, alice, serverRoom.RoomID, outliers[len(outliers)-1].EventID())

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
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		server := createTestServer(t, deployment)
		cancel := server.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, server, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, server, serverRoom, alice)
		defer psjResult.Destroy()

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
		awaitEventArrival(t, time.Second, alice, serverRoom.RoomID, sentinelEvent.EventID())

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
		testReceiveEventDuringPartialStateJoin(t, deployment, alice, psjResult, event)

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

	// when the server is in the middle of a partial state join, it should not accept
	// /make_join because it can't give a full answer.
	t.Run("Rejects make_join during partial join", func(t *testing.T) {
		// In this test, we have 3 homeservers:
		//   hs1 (the server under test) with @alice:hs1
		//     This is the server that will be in the middle of a partial join.
		//   testServer1 (a Complement test server) with @bob:<server name>
		//     This is the server that created the room originally.
		//   testServer2 (another Complement test server) with @charlie:<server name>
		//     This is the server that will try to make a join via testServer1.
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		testServer1 := createTestServer(t, deployment)
		cancel := testServer1.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, testServer1, alice.GetDefaultRoomVersion(t))
		roomID := serverRoom.RoomID
		psjResult := beginPartialStateJoin(t, testServer1, serverRoom, alice)
		defer psjResult.Destroy()

		// The partial join is now in progress.
		// Let's have a new test server rock up and ask to join the room by making a
		// /make_join request.

		testServer2 := createTestServer(t, deployment)
		cancel2 := testServer2.Listen()
		defer cancel2()

		fedClient2 := testServer2.FederationClient(deployment)

		// charlie sends a make_join
		_, err := fedClient2.MakeJoin(context.Background(), "hs1", roomID, testServer2.UserID("charlie"), federation.SupportedRoomVersions())

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
		//   hs1 (the server under test) with @alice:hs1
		//     This is the server that will be in the middle of a partial join.
		//   testServer1 (a Complement test server) with @charlie:<server name>
		//     This is the server that will create the room originally.
		//   testServer2 (another Complement test server) with @daniel:<server name>
		//     This is the server that will try to join the room via hs2,
		//     but only after using hs1 to /make_join (as otherwise we have no way
		//     of being able to build a request to /send_join)
		//
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		testServer1 := createTestServer(t, deployment)
		cancel := testServer1.Listen()
		defer cancel()
		serverRoom := createTestRoom(t, testServer1, alice.GetDefaultRoomVersion(t))
		psjResult := beginPartialStateJoin(t, testServer1, serverRoom, alice)
		defer psjResult.Destroy()

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

		// charlie then tries to /send_join via the homeserver under test
		joinEvent, err := makeJoinResp.JoinEvent.Build(time.Now(), gomatrixserverlib.ServerName(testServer2.ServerName()), testServer2.KeyID, testServer2.Priv, makeJoinResp.RoomVersion)
		must.NotError(t, "JoinEvent.Build", err)

		// SendJoin should return a 404 because the homeserver under test has not
		// finished its partial join.
		_, err = fedClient2.SendJoin(context.Background(), "hs1", joinEvent)
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
}

// test reception of an event over federation during a resync
// sends the given event to the homeserver under test, checks that a client can see it and checks
// the state at the event
func testReceiveEventDuringPartialStateJoin(
	t *testing.T, deployment *docker.Deployment, alice *client.CSAPI, psjResult partialStateJoinResult, event *gomatrixserverlib.Event,
) {
	// send the event to the homeserver
	psjResult.Server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{event.JSON()}, nil)

	awaitEventArrival(t, time.Second, alice, psjResult.ServerRoom.RoomID, event.EventID())

	// fire off a /state_ids request for the last event.
	// it must either:
	//   * block because the homeserver does not have full state at the last event
	//   * or 403 because the homeserver does not have full state yet and does not consider the
	//     Complement homeserver to be in the room
	// Synapse's behaviour will likely change once https://github.com/matrix-org/synapse/issues/13288
	// is resolved. For now, we use this to check whether Synapse has calculated the partial state
	// flag for the last event correctly.

	stateReq := gomatrixserverlib.NewFederationRequest("GET", "hs1",
		fmt.Sprintf("/_matrix/federation/v1/state_ids/%s?event_id=%s",
			url.PathEscape(psjResult.ServerRoom.RoomID),
			url.QueryEscape(event.EventID()),
		),
	)
	var respStateIDs gomatrixserverlib.RespStateIDs
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := psjResult.Server.SendFederationRequest(ctx, deployment, stateReq, &respStateIDs)
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

	// check the server's idea of the state at the event. We do this by making a `state_ids` request over federation
	stateReq = gomatrixserverlib.NewFederationRequest("GET", "hs1",
		fmt.Sprintf("/_matrix/federation/v1/state_ids/%s?event_id=%s",
			url.PathEscape(psjResult.ServerRoom.RoomID),
			url.QueryEscape(event.EventID()),
		),
	)
	if err := psjResult.Server.SendFederationRequest(context.Background(), deployment, stateReq, &respStateIDs); err != nil {
		t.Errorf("/state_ids request returned non-200: %s", err)
		return
	}
	var gotState, expectedState []interface{}
	for _, ev := range respStateIDs.StateEventIDs {
		gotState = append(gotState, ev)
	}
	for _, ev := range psjResult.ServerRoom.AllCurrentState() {
		expectedState = append(expectedState, ev.EventID())
	}
	must.CheckOffAll(t, gotState, expectedState)
}

// awaitEventArrival waits for alice to be able to see a given event
func awaitEventArrival(t *testing.T, timeout time.Duration, alice *client.CSAPI, roomID string, eventID string) {
	/* TODO: check that a lazy-loading sync can see the event. Currently this doesn't work, because /sync blocks.
	 * https://github.com/matrix-org/synapse/issues/13146
	alice.MustSyncUntil(t,
		client.SyncReq{
			Filter: buildLazyLoadingSyncFilter(nil),
		},
		client.SyncTimelineHasEventID(roomID, eventID),
	)
	*/

	// still, Alice should be able to see the event with an /event request. We might have to try it a few times.
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
	t.Logf("Alice successfully received event %s", eventID)
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
	}
	success := false
	defer func() {
		if !success {
			result.Destroy()
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
func (psj *partialStateJoinResult) Destroy() {
	if psj.fedStateIdsSendResponseWaiter != nil {
		psj.fedStateIdsSendResponseWaiter.Finish()
	}

	if psj.fedStateIdsRequestReceivedWaiter != nil {
		psj.fedStateIdsRequestReceivedWaiter.Finish()
	}
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
