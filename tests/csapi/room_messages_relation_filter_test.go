//go:build msc3874
// +build msc3874

package csapi_tests

import (
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

func TestFilterMessagesByRelType(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // flakey
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})

	// A token which only has the messages of interest after it.
	beforeToken := alice.MustSyncUntil(t, client.SyncReq{})

	// Send messages with different relations.
	res := alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", "txn-1"}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "Message without a relation",
	}))
	rootEventID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	res = alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", "txn-2"}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "Threaded Reply",
		"m.relates_to": map[string]interface{}{
			"event_id": rootEventID,
			"rel_type": "m.thread",
		},
	}))
	threadEventID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	res = alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", "txn-3"}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "Reference Reply",
		"m.relates_to": map[string]interface{}{
			"event_id": rootEventID,
			"rel_type": "m.reference",
		},
	}))
	referenceEventID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	// sync until the server has processed it
	alice.MustSyncUntil(t, client.SyncReq{Since: beforeToken}, client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		return r.Get("type").Str == "m.room.message" && r.Get("event_id").Str == referenceEventID
	}))

	// Filter to only threaded events.
	queryParams := url.Values{}
	queryParams.Set("dir", "f")
	queryParams.Set("from", beforeToken)
	queryParams.Set("filter", `{ "org.matrix.msc3874.rel_types" : ["m.thread"] }`)
	res = alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
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

	// Filtering to only references.
	queryParams = url.Values{}
	queryParams.Set("dir", "f")
	queryParams.Set("from", beforeToken)
	queryParams.Set("filter", `{ "org.matrix.msc3874.rel_types" : ["m.reference"] }`)
	res = alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOff("chunk", []interface{}{
				referenceEventID,
			}, func(r gjson.Result) interface{} {
				return r.Get("event_id").Str
			}, nil,
			),
		},
	})

	// Filter to references or threads.
	queryParams = url.Values{}
	queryParams.Set("dir", "f")
	queryParams.Set("from", beforeToken)
	queryParams.Set("filter", `{ "org.matrix.msc3874.rel_types" : ["m.thread", "m.reference"] }`)
	res = alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOff("chunk", []interface{}{
				threadEventID, referenceEventID,
			}, func(r gjson.Result) interface{} {
				return r.Get("event_id").Str
			}, nil,
			),
		},
	})

	// Filter to not threaded events.
	queryParams = url.Values{}
	queryParams.Set("dir", "f")
	queryParams.Set("from", beforeToken)
	queryParams.Set("filter", `{ "org.matrix.msc3874.not_rel_types" : ["m.thread"] }`)
	res = alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOff("chunk", []interface{}{
				rootEventID, referenceEventID,
			}, func(r gjson.Result) interface{} {
				return r.Get("event_id").Str
			}, nil,
			),
		},
	})

	// Filtering to not references.
	queryParams = url.Values{}
	queryParams.Set("dir", "f")
	queryParams.Set("from", beforeToken)
	queryParams.Set("filter", `{ "org.matrix.msc3874.not_rel_types" : ["m.reference"] }`)
	res = alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOff("chunk", []interface{}{
				rootEventID, threadEventID,
			}, func(r gjson.Result) interface{} {
				return r.Get("event_id").Str
			}, nil,
			),
		},
	})

	// Filter to not references or threads.
	queryParams = url.Values{}
	queryParams.Set("dir", "f")
	queryParams.Set("from", beforeToken)
	queryParams.Set("filter", `{ "org.matrix.msc3874.not_rel_types" : ["m.thread", "m.reference"] }`)
	res = alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOff("chunk", []interface{}{
				rootEventID,
			}, func(r gjson.Result) interface{} {
				return r.Get("event_id").Str
			}, nil,
			),
		},
	})

}
