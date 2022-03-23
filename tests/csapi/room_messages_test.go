package csapi_tests

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/matrix-org/complement/runtime"
)

// sytest: POST /rooms/:room_id/send/:event_type sends a message
// sytest: GET /rooms/:room_id/messages returns a message
func TestSendAndFetchMessage(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // flakey
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})

	const testMessage = "TestSendAndFetchMessage"

	_, token := alice.MustSync(t, client.SyncReq{})

	// first use the non-txn endpoint
	alice.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "rooms", roomID, "send", "m.room.message"}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    testMessage,
	}))

	// sync until the server has processed it
	alice.MustSyncUntil(t, client.SyncReq{Since: token}, client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		return r.Get("type").Str == "m.room.message" && r.Get("content").Get("body").Str == testMessage
	}))

	// then request messages from the room
	queryParams := url.Values{}
	queryParams.Set("dir", "f")
	queryParams.Set("from", token)
	res := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONArrayEach("chunk", func(r gjson.Result) error {
				if r.Get("type").Str == "m.room.message" && r.Get("content").Get("body").Str == testMessage {
					return nil
				}
				return fmt.Errorf("did not find correct event")
			}),
		},
	})
}

// sytest: PUT /rooms/:room_id/send/:event_type/:txn_id sends a message
// sytest: PUT /rooms/:room_id/send/:event_type/:txn_id deduplicates the same txn id
func TestSendMessageWithTxn(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{})

	const txnID = "lorem"

	res := alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "r0", "rooms", roomID, "send", "m.room.message", txnID}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "test",
	}))
	eventID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	res = alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "r0", "rooms", roomID, "send", "m.room.message", txnID}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "test",
	}))
	eventID2 := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	if eventID != eventID2 {
		t.Fatalf("two /send requests with same txn ID resulted in different event IDs")
	}
}

func TestRoomMessagesLazyLoading(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/2257

	deployment := Deploy(t, b.MustValidate(b.Blueprint{
		Name: "alice_bob_and_charlie",
		Homeservers: []b.Homeserver{
			{
				Name: "hs1",
				Users: []b.User{
					{
						Localpart:   "@alice",
						DisplayName: "Alice",
					},
					{
						Localpart:   "@bob",
						DisplayName: "Bob",
					},
					{
						Localpart:   "@charlie",
						DisplayName: "Charlie",
					},
				},
			},
		},
	}))
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")
	charlie := deployment.Client(t, "hs1", "@charlie:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.JoinRoom(t, roomID, nil)
	charlie.JoinRoom(t, roomID, nil)

	bob.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "test",
		},
	})

	beforeToken := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID), client.SyncJoinedTo(charlie.UserID, roomID))

	eventID := charlie.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "test",
		},
	})

	afterToken := alice.MustSyncUntil(t, client.SyncReq{Since: beforeToken}, client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		return r.Get("event_id").Str == eventID
	}))

	queryParams := url.Values{}
	queryParams.Set("dir", "f")
	queryParams.Set("filter", `{ "lazy_load_members" : true }`)
	queryParams.Set("from", beforeToken)
	queryParams.Set("to", afterToken)
	res := alice.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONKeyArrayOfSize("state", 1),
			match.JSONArrayEach("state", func(j gjson.Result) error {
				if j.Get("type").Str != "m.room.member" {
					return fmt.Errorf("state event is not m.room.member")
				}
				if j.Get("state_key").Str != charlie.UserID {
					return fmt.Errorf("m.room.member event is not for charlie")
				}
				if j.Get("content").Get("membership").Str != "join" {
					return fmt.Errorf("m.room.member event is not 'join'")
				}
				return nil
			}),
		},
	})

}

// TODO We should probably see if this should be removed.
//  Sytest tests for a very specific bug; check if local user member event loads properly when going backwards from a prev_event.
//  However, note that the sytest *only* checks for the *local, single* user in a room to be included in `/messages`.
//  This function exists here for sytest exhaustiveness, but I question its usefulness, thats why TestRoomMessagesLazyLoading
//  exists to do a more generic check.
//  We should probably see if this should be removed.
// sytest: GET /rooms/:room_id/messages lazy loads members correctly
func TestRoomMessagesLazyLoadingLocalUser(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{})

	token := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

	alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "test",
		},
	})

	queryParams := url.Values{}
	queryParams.Set("dir", "b")
	queryParams.Set("filter", `{ "lazy_load_members" : true }`)
	queryParams.Set("from", token)
	res := alice.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONKeyArrayOfSize("state", 1),
			match.JSONArrayEach("state", func(j gjson.Result) error {
				if j.Get("type").Str != "m.room.member" {
					return fmt.Errorf("state event is not m.room.member")
				}
				if j.Get("state_key").Str != alice.UserID {
					return fmt.Errorf("m.room.member event is not for alice")
				}
				if j.Get("content").Get("membership").Str != "join" {
					return fmt.Errorf("m.room.member event is not 'join'")
				}
				return nil
			}),
		},
	})
}
