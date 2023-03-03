package csapi_tests

import (
	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/runtime"
	"github.com/tidwall/gjson"
	"testing"
)

// TestTxnInEvent checks that the transaction ID is present when getting the event from the /rooms/{roomID}/event/{eventID} endpoint.
func TestTxnInEvent(t *testing.T) {
	// Dendrite implementation is broken
	runtime.SkipIf(t, runtime.Dendrite)

	deployment := Deploy(t, b.BlueprintCleanHS)
	defer deployment.Destroy(t)

	c := deployment.RegisterUser(t, "hs1", "alice", "password", false)

	// Create a room where we can send events.
	roomID := c.CreateRoom(t, map[string]interface{}{})

	// Let's send an event, and wait for it to appear in the timeline.
	eventID := c.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "first",
		},
	})

	// The transaction ID should be present on the GET /rooms/{roomID}/event/{eventID} response.
	res := c.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "event", eventID})
	body := client.ParseJSON(t, res)
	result := gjson.ParseBytes(body)
	if !result.Get("unsigned.transaction_id").Exists() {
		t.Fatalf("Event did not have a 'transaction_id' on the GET /rooms/%s/event/%s response", roomID, eventID)
	}
}
