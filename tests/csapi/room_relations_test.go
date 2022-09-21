package csapi_tests

import (
	"net/http"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/matrix-org/complement/runtime"
)

func TestRelations(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // not supported
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})

	const testMessage = "TestSendAndFetchMessage"

	_, token := alice.MustSync(t, client.SyncReq{})

	res := alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", "txn-1"}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "root",
	}))
	rootEventID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	// Send a few related messages.
	res = alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", "txn-2"}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "reply",
		"m.relates_to": map[string]interface{}{
			"event_id": rootEventID,
			"rel_type": "m.thread",
		},
	}))
	threadEventID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	res = alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.dummy", "txn-3"}, client.WithJSONBody(t, map[string]interface{}{
		"m.relates_to": map[string]interface{}{
			"event_id": rootEventID,
			"rel_type": "m.thread",
		},
	}))
	dummyEventID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	res = alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", "txn-4"}, client.WithJSONBody(t, map[string]interface{}{
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
	res = alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v1", "rooms", roomID, "relations", rootEventID})
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
	res = alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v1", "rooms", roomID, "relations", rootEventID, "m.thread"})
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
	res = alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v1", "rooms", roomID, "relations", rootEventID, "m.thread", "m.room.message"})
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
