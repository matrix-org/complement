package csapi_tests

import (
	"net/http"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
)

// A tuple of thread ID + latest event ID to match against.
func threadKey(threadID, latestEventID string) string {
	return threadID + "|" + latestEventID
}

func checkResults(t *testing.T, body []byte, expected []string) {
	t.Helper()

	values := gjson.GetBytes(body, "chunk")
	var result []string
	for _, v := range values.Array() {
		result = append(result, threadKey(v.Get("event_id").Str, v.Get("unsigned.m\\.relations.m\\.thread.latest_event.event_id").Str))
	}
	must.HaveInOrder(t, result, expected)
}

// Test the /threads endpoint.
func TestThreadsEndpoint(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // not supported
	deployment := complement.Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	_, token := alice.MustSync(t, client.SyncReq{})

	// Create 2 threads in the room.
	res := alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", "txn-1"}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "Thread 1 Root",
	}))
	threadID1 := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	res = alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", "txn-2"}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "Thraed 2 Root",
	}))
	threadID2 := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	// Add threaded replies.
	res = alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", "txn-3"}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "Thread 1 Reply",
		"m.relates_to": map[string]interface{}{
			"event_id": threadID1,
			"rel_type": "m.thread",
		},
	}))
	replyID1 := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	res = alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", "txn-4"}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "Thread 2 Reply",
		"m.relates_to": map[string]interface{}{
			"event_id": threadID2,
			"rel_type": "m.thread",
		},
	}))
	replyID2 := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	// sync until the server has processed it
	alice.MustSyncUntil(t, client.SyncReq{Since: token}, client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		return r.Get("event_id").Str == replyID2
	}))

	// Request the threads.
	res = alice.MustDo(t, "GET", []string{"_matrix", "client", "v1", "rooms", roomID, "threads"})
	body := must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
	})

	// Ensure the threads were properly ordered.
	checkResults(t, body, []string{threadKey(threadID2, replyID2), threadKey(threadID1, replyID1)})

	// Update thread 1 and ensure it gets updated.
	res = alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", "txn-5"}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "Thread 1 Reply 2",
		"m.relates_to": map[string]interface{}{
			"event_id": threadID1,
			"rel_type": "m.thread",
		},
	}))
	replyID3 := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	// sync until the server has processed it
	alice.MustSyncUntil(t, client.SyncReq{Since: token}, client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		return r.Get("event_id").Str == replyID3
	}))

	res = alice.MustDo(t, "GET", []string{"_matrix", "client", "v1", "rooms", roomID, "threads"})
	body = must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
	})

	// Ensure the threads were properly ordered.
	checkResults(t, body, []string{threadKey(threadID1, replyID3), threadKey(threadID2, replyID2)})
}
