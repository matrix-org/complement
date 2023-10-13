package csapi_tests

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
)

// sytest: POST /rooms/:room_id/send/:event_type sends a message
// sytest: GET /rooms/:room_id/messages returns a message
func TestSendAndFetchMessage(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // flakey
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})

	const testMessage = "TestSendAndFetchMessage"

	_, token := alice.MustSync(t, client.SyncReq{})

	// first use the non-txn endpoint
	alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message"}, client.WithJSONBody(t, map[string]interface{}{
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
	res := alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
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

// With a non-existent room_id, GET /rooms/:room_id/messages returns 403
// forbidden ("You aren't a member of the room").
func TestFetchMessagesFromNonExistentRoom(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	roomID := "!does-not-exist:hs1"

	// then request messages from the room
	queryParams := url.Values{}
	queryParams.Set("dir", "b")
	res := alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusForbidden,
	})
}

// sytest: PUT /rooms/:room_id/send/:event_type/:txn_id sends a message
// sytest: PUT /rooms/:room_id/send/:event_type/:txn_id deduplicates the same txn id
func TestSendMessageWithTxn(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{})

	const txnID = "lorem"

	res := alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", txnID}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "test",
	}))
	eventID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	res = alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", txnID}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "test",
	}))
	eventID2 := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	if eventID != eventID2 {
		t.Fatalf("two /send requests with same txn ID resulted in different event IDs")
	}
}

func TestRoomMessagesLazyLoading(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	charlie := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.MustJoinRoom(t, roomID, nil)
	charlie.MustJoinRoom(t, roomID, nil)

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
	res := alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
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
//
//	Sytest tests for a very specific bug; check if local user member event loads properly when going backwards from a prev_event.
//	However, note that the sytest *only* checks for the *local, single* user in a room to be included in `/messages`.
//	This function exists here for sytest exhaustiveness, but I question its usefulness, thats why TestRoomMessagesLazyLoading
//	exists to do a more generic check.
//	We should probably see if this should be removed.
//
// sytest: GET /rooms/:room_id/messages lazy loads members correctly
func TestRoomMessagesLazyLoadingLocalUser(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{})

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
	res := alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
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
