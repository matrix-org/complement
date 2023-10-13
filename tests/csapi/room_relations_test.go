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

func TestRelations(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	_, token := alice.MustSync(t, client.SyncReq{})

	res := alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", "txn-1"}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "root",
	}))
	rootEventID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	// Send a few related messages.
	res = alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", "txn-2"}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "reply",
		"m.relates_to": map[string]interface{}{
			"event_id": rootEventID,
			"rel_type": "m.thread",
		},
	}))
	threadEventID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	res = alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.dummy", "txn-3"}, client.WithJSONBody(t, map[string]interface{}{
		"m.relates_to": map[string]interface{}{
			"event_id": rootEventID,
			"rel_type": "m.thread",
		},
	}))
	dummyEventID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	res = alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", "txn-4"}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "* edited root",
		"m.new_content": map[string]interface{}{
			"msgtype": "m.text",
			"body":    "edited root",
		},
		"m.relates_to": map[string]interface{}{
			"event_id": rootEventID,
			"rel_type": "m.replace",
		},
	}))
	editEventID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	// sync until the server has processed it
	alice.MustSyncUntil(t, client.SyncReq{Since: token}, client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		return r.Get("event_id").Str == editEventID
	}))

	// Request the relations.
	res = alice.MustDo(t, "GET", []string{"_matrix", "client", "v1", "rooms", roomID, "relations", rootEventID})
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOff("chunk", []interface{}{
				threadEventID, dummyEventID, editEventID,
			}, func(r gjson.Result) interface{} {
				return r.Get("event_id").Str
			}, nil,
			),
		},
	})

	// Also test filtering by the relation type.
	res = alice.MustDo(t, "GET", []string{"_matrix", "client", "v1", "rooms", roomID, "relations", rootEventID, "m.thread"})
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOff("chunk", []interface{}{
				threadEventID, dummyEventID,
			}, func(r gjson.Result) interface{} {
				return r.Get("event_id").Str
			}, nil,
			),
		},
	})

	// And test filtering by relation type + event type.
	res = alice.MustDo(t, "GET", []string{"_matrix", "client", "v1", "rooms", roomID, "relations", rootEventID, "m.thread", "m.room.message"})
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOff("chunk", []interface{}{
				threadEventID,
			}, func(r gjson.Result) interface{} {
				return r.Get("event_id").Str
			}, nil,
			),
		},
	})
}

func TestRelationsPagination(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	_, token := alice.MustSync(t, client.SyncReq{})

	res := alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", "txn-1"}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "root",
	}))
	rootEventID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	// Create some related events.
	event_ids := [10]string{}
	for i := 0; i < 10; i++ {
		res = alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", fmt.Sprintf("txn-%d", 2+i)}, client.WithJSONBody(t, map[string]interface{}{
			"msgtype": "m.text",
			"body":    fmt.Sprintf("reply %d", i),
			"m.relates_to": map[string]interface{}{
				"event_id": rootEventID,
				"rel_type": "m.thread",
			},
		}))
		event_ids[i] = client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")
	}

	// sync until the server has processed it
	alice.MustSyncUntil(t, client.SyncReq{Since: token}, client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		return r.Get("event_id").Str == event_ids[9]
	}))

	// Fetch the first page.
	queryParams := url.Values{}
	queryParams.Set("limit", "3")
	res = alice.MustDo(t, "GET", []string{"_matrix", "client", "v1", "rooms", roomID, "relations", rootEventID}, client.WithQueries(queryParams))
	body := must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOff("chunk", []interface{}{
				event_ids[9], event_ids[8], event_ids[7],
			}, func(r gjson.Result) interface{} {
				return r.Get("event_id").Str
			}, nil,
			),
		},
	})

	// Fetch the next page.
	queryParams.Set("from", client.GetJSONFieldStr(t, body, "next_batch"))
	res = alice.MustDo(t, "GET", []string{"_matrix", "client", "v1", "rooms", roomID, "relations", rootEventID}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOff("chunk", []interface{}{
				event_ids[6], event_ids[5], event_ids[4],
			}, func(r gjson.Result) interface{} {
				return r.Get("event_id").Str
			}, nil,
			),
		},
	})

	// Fetch the first page in the forward direction.
	queryParams = url.Values{}
	queryParams.Set("limit", "3")
	queryParams.Set("dir", "f")
	res = alice.MustDo(t, "GET", []string{"_matrix", "client", "v1", "rooms", roomID, "relations", rootEventID}, client.WithQueries(queryParams))
	body = must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOff("chunk", []interface{}{
				event_ids[0], event_ids[1], event_ids[2],
			}, func(r gjson.Result) interface{} {
				return r.Get("event_id").Str
			}, nil,
			),
		},
	})

	// Fetch the next page in the forward direction.
	queryParams.Set("from", client.GetJSONFieldStr(t, body, "next_batch"))
	res = alice.MustDo(t, "GET", []string{"_matrix", "client", "v1", "rooms", roomID, "relations", rootEventID}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOff("chunk", []interface{}{
				event_ids[3], event_ids[4], event_ids[5],
			}, func(r gjson.Result) interface{} {
				return r.Get("event_id").Str
			}, nil,
			),
		},
	})
}

func TestRelationsPaginationSync(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/2944

	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	_, token := alice.MustSync(t, client.SyncReq{})

	rootEventID := alice.Unsafe_SendEventUnsynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "root",
		},
		Sender: alice.UserID,
	})

	// Create some related events.
	eventID := ""
	for i := 0; i < 5; i++ {
		eventID = alice.Unsafe_SendEventUnsynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    fmt.Sprintf("reply %d before sync token", i),
				"m.relates_to": map[string]interface{}{
					"event_id": rootEventID,
					"rel_type": "m.thread",
				},
			},
			Sender: alice.UserID,
		})
	}

	// Sync and keep the token.
	nextBatch := alice.MustSyncUntil(t, client.SyncReq{Since: token}, client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		return r.Get("event_id").Str == eventID
	}))

	// Create more related events.
	event_ids := [5]string{}
	for i := 0; i < 5; i++ {
		event_ids[i] = alice.Unsafe_SendEventUnsynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    fmt.Sprintf("reply %d after sync token", i),
				"m.relates_to": map[string]interface{}{
					"event_id": rootEventID,
					"rel_type": "m.thread",
				},
			},
			Sender: alice.UserID,
		})
	}

	// sync until the server has processed it
	alice.MustSyncUntil(t, client.SyncReq{Since: token}, client.SyncTimelineHasEventID(roomID, event_ids[4]))

	// Fetch the first page since the last sync.
	queryParams := url.Values{}
	queryParams.Set("limit", "3")
	queryParams.Set("from", nextBatch)
	queryParams.Set("dir", "f")
	res := alice.MustDo(t, "GET", []string{"_matrix", "client", "v1", "rooms", roomID, "relations", rootEventID}, client.WithQueries(queryParams))
	body := must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOff("chunk", []interface{}{
				event_ids[0], event_ids[1], event_ids[2],
			}, func(r gjson.Result) interface{} {
				return r.Get("event_id").Str
			}, nil,
			),
		},
	})

	// Fetch the next page.
	queryParams.Set("from", client.GetJSONFieldStr(t, body, "next_batch"))
	res = alice.MustDo(t, "GET", []string{"_matrix", "client", "v1", "rooms", roomID, "relations", rootEventID}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOff("chunk", []interface{}{
				event_ids[3], event_ids[4],
			}, func(r gjson.Result) interface{} {
				return r.Get("event_id").Str
			}, nil,
			),
		},
	})
}
