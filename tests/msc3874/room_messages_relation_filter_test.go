package tests

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
)

func TestFilterMessagesByRelType(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // flakey
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})

	// A token which only has the messages of interest after it.
	beforeToken := alice.MustSyncUntil(t, client.SyncReq{})

	// Send messages with different relations.
	res := alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", "txn-1"}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "Message without a relation",
	}))
	rootEventID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	res = alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", "txn-2"}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "Threaded Reply",
		"m.relates_to": map[string]interface{}{
			"event_id": rootEventID,
			"rel_type": "m.thread",
		},
	}))
	threadEventID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	res = alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", "txn-3"}, client.WithJSONBody(t, map[string]interface{}{
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
	res = alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOffDeprecated("chunk", []interface{}{
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
	res = alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOffDeprecated("chunk", []interface{}{
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
	res = alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOffDeprecated("chunk", []interface{}{
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
	res = alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOffDeprecated("chunk", []interface{}{
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
	res = alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOffDeprecated("chunk", []interface{}{
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
	res = alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONCheckOffDeprecated("chunk", []interface{}{
				rootEventID,
			}, func(r gjson.Result) interface{} {
				return r.Get("event_id").Str
			}, nil,
			),
		},
	})

}
