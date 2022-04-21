// +build faster_joins

// This file contains tests for joining rooms over federation, with the
// features introduced in msc2775.

package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/gomatrixserverlib"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/complement/internal/match"
)

// TestSyncBlocksDuringPartialStateJoin tests that a regular /sync request
// made during a partial-state /send_join request blocks until the state is
// correctly synced.
func TestSyncBlocksDuringPartialStateJoin(t *testing.T) {
	// We make a room on the Complement server, then have @alice:hs1 join it,
	// and make a sync request while the resync is in flight

	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandlePartialStateMakeSendJoinRequests(),
		federation.HandleEventRequests(),
	)
	cancel := srv.Listen()
	defer cancel()

	// some things for orchestration
	fedStateIdsRequestReceivedWaiter := NewWaiter()
	defer fedStateIdsRequestReceivedWaiter.Finish()
	fedStateIdsSendResponseWaiter := NewWaiter()
	defer fedStateIdsSendResponseWaiter.Finish()

	// create the room on the complement server, with charlie and derek as members
	charlie := srv.UserID("charlie")
	derek := srv.UserID("derek")
	serverRoom := makeTestRoom(t, srv, alice.GetDefaultRoomVersion(t), charlie, derek)

	// register a handler for /state_ids requests, which finishes fedStateIdsRequestReceivedWaiter, then
	// waits for fedStateIdsSendResponseWaiter and sends a reply
	handleStateIdsRequests(t, srv, serverRoom, fedStateIdsRequestReceivedWaiter, fedStateIdsSendResponseWaiter)

	// a handler for /state requests, which sends a sensible response
	handleStateRequests(t, srv, serverRoom, nil, nil)

	// have alice join the room by room ID.
	alice.JoinRoom(t, serverRoom.RoomID, []string{srv.ServerName()})
	t.Logf("Join completed")

	// Alice has now joined the room, and the server is syncing the state in the background.

	// attempts to sync should now block. Fire off a goroutine to try it.
	syncResponseChan := make(chan gjson.Result)
	defer close(syncResponseChan)
	go func() {
		response, _ := alice.MustSync(t, client.SyncReq{})
		syncResponseChan <- response
	}()

	// wait for the state_ids request to arrive
	fedStateIdsRequestReceivedWaiter.Waitf(t, 5*time.Second, "Waiting for /state_ids request")

	// the client-side requests should still be waiting
	select {
	case <-syncResponseChan:
		t.Fatalf("Sync completed before state resync complete")
	default:
	}

	// release the federation /state response
	fedStateIdsSendResponseWaiter.Finish()

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
			"m.room.member|" + charlie,
			"m.room.member|" + derek,
		}, func(result gjson.Result) interface{} {
			return strings.Join([]string{result.Map()["type"].Str, result.Map()["state_key"].Str}, "|")
		}, nil,
	)
	if err := matcher([]byte(roomRes.Raw)); err != nil {
		t.Errorf("Did not find expected state events in /sync response: %s", err)
	}
	t.Fail()
}

// makeTestRoom constructs a test room on the Complement server, and adds the given extra members
func makeTestRoom(t *testing.T, srv *federation.Server, roomVer gomatrixserverlib.RoomVersion, creator string, members ...string) *federation.ServerRoom {
	serverRoom := srv.MustMakeRoom(t, roomVer, federation.InitialRoomEvents(roomVer, creator))
	for _, m := range members {
		serverRoom.AddEvent(srv.MustCreateEvent(t, serverRoom, b.Event{
			Type:     "m.room.member",
			StateKey: b.Ptr(m),
			Sender:   m,
			Content: map[string]interface{}{
				"membership": "join",
			},
		}),
		)
	}
	return serverRoom
}

// handleStateIdsRequests registers a handler for /state_ids requests for serverRoom.
//
// if requestReceivedWaiter is not nil, it will be Finish()ed when the request arrives.
// if sendResponseWaiter is not nil, we will Wait() for it to finish before sending the response.
func handleStateIdsRequests(
	t *testing.T, srv *federation.Server, serverRoom *federation.ServerRoom,
	requestReceivedWaiter *Waiter, sendResponseWaiter *Waiter,
) {
	srv.Mux().Handle(
		fmt.Sprintf("/_matrix/federation/v1/state_ids/%s", serverRoom.RoomID),
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
				AuthEventIDs:  eventIDsFromEvents(serverRoom.AuthChain()),
				StateEventIDs: eventIDsFromEvents(serverRoom.AllCurrentState()),
			}
			w.WriteHeader(200)
			jsonb, _ := json.Marshal(res)

			if _, err := w.Write(jsonb); err != nil {
				t.Errorf("Error writing to request: %v", err)
			}
		}),
	).Methods("GET")
}

// makeStateHandler returns a handler for /state requests for serverRoom.
//
// if requestReceivedWaiter is not nil, it will be Finish()ed when the request arrives.
// if sendResponseWaiter is not nil, we will Wait() for it to finish before sending the response.
func handleStateRequests(
	t *testing.T, srv *federation.Server, serverRoom *federation.ServerRoom,
	requestReceivedWaiter *Waiter, sendResponseWaiter *Waiter,
) {
	srv.Mux().Handle(
		fmt.Sprintf("/_matrix/federation/v1/state/%s", serverRoom.RoomID),
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
				AuthEvents:  gomatrixserverlib.NewEventJSONsFromEvents(serverRoom.AuthChain()),
				StateEvents: gomatrixserverlib.NewEventJSONsFromEvents(serverRoom.AllCurrentState()),
			}
			w.WriteHeader(200)
			jsonb, _ := json.Marshal(res)

			if _, err := w.Write(jsonb); err != nil {
				t.Errorf("Error writing to request: %v", err)
			}
		}),
	).Methods("GET")
}

func eventIDsFromEvents(he []*gomatrixserverlib.Event) []string {
	eventIDs := make([]string, len(he))
	for i := range he {
		eventIDs[i] = he[i].EventID()
	}
	return eventIDs
}
