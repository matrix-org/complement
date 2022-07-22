//go:build faster_joins
// +build faster_joins

// This file contains tests for joining rooms over federation, with the
// features introduced in msc2775.

package tests

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/tidwall/gjson"

	"github.com/matrix-org/gomatrix"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/util"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestPartialStateJoin(t *testing.T) {
	// test that a regular /sync request made during a partial-state /send_join
	// request blocks until the state is correctly synced.
	t.Run("SyncBlocksDuringPartialStateJoin", func(t *testing.T) {
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		psjResult := beginPartialStateJoin(t, deployment, alice)
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

		roomRes := syncRes.Get("rooms.join." + client.GjsonEscape(psjResult.ServerRoom.RoomID))
		if !roomRes.Exists() {
			t.Fatalf("/sync completed without join to new room\n")
		}

		// check that the state includes both charlie and derek.
		matcher := match.JSONCheckOffAllowUnwanted("state.events",
			[]interface{}{
				"m.room.member|" + psjResult.Server.UserID("charlie"),
				"m.room.member|" + psjResult.Server.UserID("derek"),
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

		psjResult := beginPartialStateJoin(t, deployment, alice)
		defer psjResult.Destroy()

		alice.MustSyncUntil(t,
			client.SyncReq{
				Filter: buildLazyLoadingSyncFilter(nil),
			},
			client.SyncJoinedTo(alice.UserID, psjResult.ServerRoom.RoomID),
		)
		t.Logf("Alice successfully synced")
	})

	// we should be able to send events in the room, during the resync
	t.Run("CanSendEventsDuringPartialStateJoin", func(t *testing.T) {
		t.Skip("Cannot yet send events during resync")
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		psjResult := beginPartialStateJoin(t, deployment, alice)
		defer psjResult.Destroy()

		alice.Client.Timeout = 2 * time.Second
		paths := []string{"_matrix", "client", "v3", "rooms", psjResult.ServerRoom.RoomID, "send", "m.room.message", "0"}
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

		psjResult := beginPartialStateJoin(t, deployment, alice)
		defer psjResult.Destroy()

		// the HS will make an /event_auth request for the event
		federation.HandleEventAuthRequests()(psjResult.Server)

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

		psjResult := beginPartialStateJoin(t, deployment, alice)
		defer psjResult.Destroy()

		// we construct the following event graph:
		// ... <-- M <-- A <-- B
		//
		// M is @alice:hs1's join event.
		// A and B are regular m.room.messsage events created by @derek on the Complement homeserver.
		//
		// initially, hs1 only knows about event M.
		// we send only event B to hs1.
		eventM := psjResult.ServerRoom.CurrentState("m.room.member", alice.UserID)
		eventA := psjResult.CreateMessageEvent(t, "derek", []string{eventM.EventID()})
		eventB := psjResult.CreateMessageEvent(t, "derek", []string{eventA.EventID()})
		t.Logf("%s's m.room.member event is %s", *eventM.StateKey(), eventM.EventID())
		t.Logf("Derek created event A with ID %s", eventA.EventID())
		t.Logf("Derek created event B with ID %s", eventB.EventID())

		// the HS will make an /event_auth request for event A
		federation.HandleEventAuthRequests()(psjResult.Server)

		// the HS will make a /get_missing_events request for the missing prev events of event B
		handleGetMissingEventsRequests(t, psjResult.Server, psjResult.ServerRoom, []*gomatrixserverlib.Event{eventA})

		// send event B to hs1
		testReceiveEventDuringPartialStateJoin(t, deployment, alice, psjResult, eventB)
	})

	// we should be able to receive events with partially missing prev events over federation during the resync
	t.Run("CanReceiveEventsWithHalfMissingParentsDuringPartialStateJoin", func(t *testing.T) {
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		psjResult := beginPartialStateJoin(t, deployment, alice)
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
		eventM := psjResult.ServerRoom.CurrentState("m.room.member", alice.UserID)
		eventA := psjResult.CreateMessageEvent(t, "derek", []string{eventM.EventID()})
		eventB := psjResult.CreateMessageEvent(t, "derek", []string{eventA.EventID(), eventM.EventID()})
		t.Logf("%s's m.room.member event is %s", *eventM.StateKey(), eventM.EventID())
		t.Logf("Derek created event A with ID %s", eventA.EventID())
		t.Logf("Derek created event B with ID %s", eventB.EventID())

		// the HS will make an /event_auth request for event A
		federation.HandleEventAuthRequests()(psjResult.Server)

		// the HS will make a /get_missing_events request for the missing prev event of event B
		handleGetMissingEventsRequests(t, psjResult.Server, psjResult.ServerRoom, []*gomatrixserverlib.Event{eventA})

		// send event B to hs1
		testReceiveEventDuringPartialStateJoin(t, deployment, alice, psjResult, eventB)
	})

	// we should be able to receive events with a missing prev event, with half missing prev events,
	// over federation during the resync
	t.Run("CanReceiveEventsWithHalfMissingGrandparentsDuringPartialStateJoin", func(t *testing.T) {
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		psjResult := beginPartialStateJoin(t, deployment, alice)
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
		eventM := psjResult.ServerRoom.CurrentState("m.room.member", alice.UserID)
		eventA := psjResult.CreateMessageEvent(t, "derek", []string{eventM.EventID()})
		eventB := psjResult.CreateMessageEvent(t, "derek", []string{eventA.EventID(), eventM.EventID()})
		eventC := psjResult.CreateMessageEvent(t, "derek", []string{eventB.EventID()})
		t.Logf("%s's m.room.member event is %s", *eventM.StateKey(), eventM.EventID())
		t.Logf("Derek created event A with ID %s", eventA.EventID())
		t.Logf("Derek created event B with ID %s", eventB.EventID())
		t.Logf("Derek created event C with ID %s", eventC.EventID())

		// the HS will make a /get_missing_events request for the missing prev event of event C,
		// to which we respond with event B only.
		handleGetMissingEventsRequests(t, psjResult.Server, psjResult.ServerRoom, []*gomatrixserverlib.Event{eventB})

		// dedicated state_ids and state handlers for event A
		handleStateIdsRequests(t, psjResult.Server, psjResult.ServerRoom, eventA.EventID(), psjResult.ServerRoom.AllCurrentState(), nil, nil)
		handleStateRequests(t, psjResult.Server, psjResult.ServerRoom, eventA.EventID(), psjResult.ServerRoom.AllCurrentState(), nil, nil)

		// send event C to hs1
		testReceiveEventDuringPartialStateJoin(t, deployment, alice, psjResult, eventC)
	})

	// a request to (client-side) /members?at= should block until the (federation) /state request completes
	// TODO(faster_joins): also need to test /state, and /members without an `at`, which follow a different path
	t.Run("MembersRequestBlocksDuringPartialStateJoin", func(t *testing.T) {
		deployment := Deploy(t, b.BlueprintAlice)
		defer deployment.Destroy(t)
		alice := deployment.Client(t, "hs1", "@alice:hs1")

		psjResult := beginPartialStateJoin(t, deployment, alice)
		defer psjResult.Destroy()

		// we need a sync token to pass to the `at` param.
		syncToken := alice.MustSyncUntil(t,
			client.SyncReq{
				Filter: buildLazyLoadingSyncFilter(nil),
			},
			client.SyncJoinedTo(alice.UserID, psjResult.ServerRoom.RoomID),
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
				[]string{"_matrix", "client", "v3", "rooms", psjResult.ServerRoom.RoomID, "members"},
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
							"m.room.member|" + psjResult.Server.UserID("charlie"),
							"m.room.member|" + psjResult.Server.UserID("derek"),
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

		psjResult := beginPartialStateJoin(t, deployment, alice)
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

		roomRes := syncRes.Get("rooms.join." + client.GjsonEscape(psjResult.ServerRoom.RoomID))
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
		server := federation.NewServer(t, deployment,
			federation.HandleKeyRequests(),
			federation.HandlePartialStateMakeSendJoinRequests(),
			federation.HandleEventRequests(),
			federation.HandleTransactionRequests(
				func(e *gomatrixserverlib.Event) {
					t.Fatalf("Received unexpected PDU: %s", string(e.JSON()))
				},
				// hs1 may send us presence when alice syncs
				nil,
			),
		)
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

		psjResult := beginPartialStateJoin(t, deployment, alice)
		defer psjResult.Destroy()

		// get a sync token before state syncing finishes.
		syncToken := alice.MustSyncUntil(t,
			client.SyncReq{
				Filter: buildLazyLoadingSyncFilter(nil),
			},
			client.SyncJoinedTo(alice.UserID, psjResult.ServerRoom.RoomID),
		)
		t.Logf("Alice successfully synced")

		// wait for partial state to finish syncing,
		// by waiting for the room to show up in a regular /sync.
		psjResult.AwaitStateIdsRequest(t)
		psjResult.FinishStateRequest()
		alice.MustSyncUntil(t,
			client.SyncReq{},
			client.SyncJoinedTo(alice.UserID, psjResult.ServerRoom.RoomID),
		)

		// make derek send two messages into the room.
		// we will do a gappy sync after, which will only pick up the last message.
		var lastEventID string
		for i := 0; i < 2; i++ {
			event := psjResult.Server.MustCreateEvent(t, psjResult.ServerRoom, b.Event{
				Type:   "m.room.message",
				Sender: psjResult.Server.UserID("derek"),
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    "Message " + strconv.Itoa(i),
				},
			})
			lastEventID = event.EventID()
			psjResult.ServerRoom.AddEvent(event)
			psjResult.Server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{event.JSON()}, nil)
		}

		// wait for the events to come down a regular /sync.
		alice.MustSyncUntil(t,
			client.SyncReq{},
			client.SyncTimelineHasEventID(psjResult.ServerRoom.RoomID, lastEventID),
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
		roomRes := syncRes.Get("rooms.join." + client.GjsonEscape(psjResult.ServerRoom.RoomID))
		if !roomRes.Exists() {
			t.Fatalf("/sync completed without join to new room\n")
		}
		t.Logf("gappy /sync response for %s: %s", psjResult.ServerRoom.RoomID, roomRes)

		timelineMatcher := match.JSONCheckOff("timeline.events",
			[]interface{}{lastEventID},
			func(result gjson.Result) interface{} {
				return result.Map()["event_id"].Str
			}, nil,
		)
		stateMatcher := match.JSONCheckOffAllowUnwanted("state.events",
			[]interface{}{
				"m.room.member|" + psjResult.Server.UserID("derek"),
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
}

// test reception of an event over federation during a resync
// sends the given event to the homeserver under test, checks that a client can see it and checks
// the state at the event
func testReceiveEventDuringPartialStateJoin(
	t *testing.T, deployment *docker.Deployment, alice *client.CSAPI, psjResult partialStateJoinResult, event *gomatrixserverlib.Event,
) {
	// send the event to the homeserver
	psjResult.Server.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{event.JSON()}, nil)

	/* TODO: check that a lazy-loading sync can see the event. Currently this doesn't work, because /sync blocks.
	 * https://github.com/matrix-org/synapse/issues/13146
	alice.MustSyncUntil(t,
		client.SyncReq{
			Filter: buildLazyLoadingSyncFilter(nil),
		},
		client.SyncTimelineHasEventID(psjResult.ServerRoom.RoomID, event.EventID()),
	)
	*/

	// still, Alice should be able to see the event with an /event request. We might have to try it a few times.
	start := time.Now()
	for {
		if time.Since(start) > time.Second {
			t.Fatalf("timeout waiting for received event to be visible")
		}
		res := alice.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", psjResult.ServerRoom.RoomID, "event", event.EventID()})
		eventResBody := client.ParseJSON(t, res)
		if res.StatusCode == 200 {
			t.Logf("Successfully fetched received event %s", event.EventID())
			break
		}
		if res.StatusCode == 404 && gjson.GetBytes(eventResBody, "errcode").String() == "M_NOT_FOUND" {
			t.Logf("Fetching received event failed with M_NOT_FOUND; will retry")
			time.Sleep(100 * time.Millisecond)
			continue
		}
		t.Fatalf("GET /event failed with %d: %s", res.StatusCode, string(eventResBody))
	}

	// fire off a /state_ids request for the last event.
	// it must either:
	//   * block because the homeserver does not have full state at the last event
	//   * or 403 because the homeserver does not have full state yet and does not consider the
	//     Complement homeserver to be in the room

	type StateIDsResult struct {
		RespStateIDs gomatrixserverlib.RespStateIDs
		Error        error
	}
	stateIdsResultChan := make(chan StateIDsResult)
	defer close(stateIdsResultChan)
	go func() {
		stateReq := gomatrixserverlib.NewFederationRequest("GET", "hs1",
			fmt.Sprintf("/_matrix/federation/v1/state_ids/%s?event_id=%s",
				url.PathEscape(psjResult.ServerRoom.RoomID),
				url.QueryEscape(event.EventID()),
			),
		)
		var respStateIDs gomatrixserverlib.RespStateIDs
		if err := psjResult.Server.SendFederationRequest(deployment, stateReq, &respStateIDs); err != nil {
			stateIdsResultChan <- StateIDsResult{Error: err}
		} else {
			stateIdsResultChan <- StateIDsResult{RespStateIDs: respStateIDs}
		}
	}()

	select {
	case <-time.After(1 * time.Second):
		t.Logf("/state_ids request for event %s blocked as expected", event.EventID())
		defer func() { <-stateIdsResultChan }()
		break
	case stateIDsResult := <-stateIdsResultChan:
		if stateIDsResult.Error != nil {
			httpErr, ok := stateIDsResult.Error.(gomatrix.HTTPError)
			t.Logf("%v", httpErr)
			if ok && httpErr.Code == 403 {
				t.Logf("/state_ids request for event %s returned 403 as expected", event.EventID())
			} else {
				t.Errorf("/state_ids request returned non-200: %s", stateIDsResult.Error)
			}
		} else {
			// since we have not yet given the homeserver the full state at the join event and allowed
			// the partial join to complete, it can't possibly know the full state at the last event.
			// While it may be possible for the response to be correct by some accident of state res,
			// the homeserver is still wrong in spirit.
			t.Fatalf("/state_ids request for event %s did not block when it should have", event.EventID())
		}
	}

	// allow the partial join to complete
	psjResult.FinishStateRequest()
	alice.MustSyncUntil(t,
		client.SyncReq{},
		client.SyncJoinedTo(alice.UserID, psjResult.ServerRoom.RoomID),
	)

	// check the server's idea of the state at the event. We do this by making a `state_ids` request over federation
	stateReq := gomatrixserverlib.NewFederationRequest("GET", "hs1",
		fmt.Sprintf("/_matrix/federation/v1/state_ids/%s?event_id=%s",
			url.PathEscape(psjResult.ServerRoom.RoomID),
			url.QueryEscape(event.EventID()),
		),
	)
	var respStateIDs gomatrixserverlib.RespStateIDs
	if err := psjResult.Server.SendFederationRequest(deployment, stateReq, &respStateIDs); err != nil {
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
	cancelListener                   func()
	Server                           *federation.Server
	ServerRoom                       *federation.ServerRoom
	fedStateIdsRequestReceivedWaiter *Waiter
	fedStateIdsSendResponseWaiter    *Waiter
}

// beginPartialStateJoin spins up a room on a complement server,
// then has a test user join it. It returns a partialStateJoinResult,
// which must be Destroy'd on completion.
//
// When this method completes, the /join request will have completed, but the
// state has not yet been re-synced. To allow the re-sync to proceed, call
// partialStateJoinResult.FinishStateRequest.
func beginPartialStateJoin(t *testing.T, deployment *docker.Deployment, joiningUser *client.CSAPI) partialStateJoinResult {
	result := partialStateJoinResult{}
	success := false
	defer func() {
		if !success {
			result.Destroy()
		}
	}()

	result.Server = federation.NewServer(t, deployment,
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
	result.cancelListener = result.Server.Listen()

	// some things for orchestration
	result.fedStateIdsRequestReceivedWaiter = NewWaiter()
	result.fedStateIdsSendResponseWaiter = NewWaiter()

	// create the room on the complement server, with charlie and derek as members
	roomVer := joiningUser.GetDefaultRoomVersion(t)
	result.ServerRoom = result.Server.MustMakeRoom(t, roomVer, federation.InitialRoomEvents(roomVer, result.Server.UserID("charlie")))
	result.ServerRoom.AddEvent(result.Server.MustCreateEvent(t, result.ServerRoom, b.Event{
		Type:     "m.room.member",
		StateKey: b.Ptr(result.Server.UserID("derek")),
		Sender:   result.Server.UserID("derek"),
		Content: map[string]interface{}{
			"membership": "join",
		},
	}))

	// register a handler for /state_ids requests for the most recent event,
	// which finishes fedStateIdsRequestReceivedWaiter, then
	// waits for fedStateIdsSendResponseWaiter and sends a reply
	lastEvent := result.ServerRoom.Timeline[len(result.ServerRoom.Timeline)-1]
	currentState := result.ServerRoom.AllCurrentState()
	handleStateIdsRequests(
		t, result.Server, result.ServerRoom,
		lastEvent.EventID(), currentState,
		result.fedStateIdsRequestReceivedWaiter, result.fedStateIdsSendResponseWaiter,
	)

	// a handler for /state requests, which sends a sensible response
	handleStateRequests(
		t, result.Server, result.ServerRoom,
		lastEvent.EventID(), currentState,
		nil, nil,
	)

	// have joiningUser join the room by room ID.
	joiningUser.JoinRoom(t, result.ServerRoom.RoomID, []string{result.Server.ServerName()})
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

	if psj.cancelListener != nil {
		psj.cancelListener()
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
			t.Logf("Replying to /state_ids request")

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
}

// register a handler for `/get_missing_events` requests
func handleGetMissingEventsRequests(
	t *testing.T, srv *federation.Server, serverRoom *federation.ServerRoom,
	eventsToReturn []*gomatrixserverlib.Event,
) {
	srv.Mux().HandleFunc("/_matrix/federation/v1/get_missing_events/{roomID}", func(w http.ResponseWriter, req *http.Request) {
		roomID := mux.Vars(req)["roomID"]
		if roomID != serverRoom.RoomID {
			t.Fatalf("Received unexpected /get_missing_events request for room: %s", roomID)
		}

		body, err := ioutil.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("unable to read request body: %v", err)
		}
		var getMissingEventsRequest struct {
			EarliestEvents []string `json:"earliest_events"`
			LatestEvents   []string `json:"latest_events"`
			Limit          int      `json:"int"`
			MinDepth       int      `json:"min_depth"`
		}
		err = json.Unmarshal(body, &getMissingEventsRequest)
		if err != nil {
			errResp := util.MessageResponse(400, err.Error())
			w.WriteHeader(errResp.Code)
			b, _ := json.Marshal(errResp.JSON)
			w.Write(b)
			return
		}

		t.Logf("Incoming get_missing_events request for prev events of %s in room %s", getMissingEventsRequest.LatestEvents, roomID)

		// TODO: return events based on those requested
		w.WriteHeader(200)
		res := struct {
			Events []*gomatrixserverlib.Event `json:"events"`
		}{
			Events: eventsToReturn,
		}
		responseBytes, _ := json.Marshal(&res)
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
