package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/runtime"
	"github.com/tidwall/gjson"
)

// TestTxnInEvent checks that the transaction ID is present when getting the event from the /rooms/{roomID}/event/{eventID} endpoint.
func TestTxnInEvent(t *testing.T) {
	// Dendrite implementation is broken
	// See https://github.com/matrix-org/dendrite/issues/3000
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


func mustHaveTransactionID(t *testing.T, roomID, eventID string) client.SyncCheckOpt {
	return client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		if r.Get("event_id").Str == eventID {
			if !r.Get("unsigned.transaction_id").Exists() {
				t.Fatalf("Event %s in room %s should have a 'transaction_id', but it did not", eventID, roomID)
			}

			return true
		}

		return false
	})
}

func mustNotHaveTransactionID(t *testing.T, roomID, eventID string) client.SyncCheckOpt {
	return client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		if r.Get("event_id").Str == eventID {
			res := r.Get("unsigned.transaction_id")
			if res.Exists() {
				t.Fatalf("Event %s in room %s should NOT have a 'transaction_id', but it did (%s)", eventID, roomID, res.Str)
			}

			return true
		}

		return false
	})
}

// TestTxnScopeOnLocalEcho tests that transaction IDs are scoped to the access token, not the device
// on the sync response
func TestTxnScopeOnLocalEcho(t *testing.T) {
	// Conduit scope transaction IDs to the device ID, not the access token.
	runtime.SkipIf(t, runtime.Conduit)

	deployment := Deploy(t, b.BlueprintCleanHS)
	defer deployment.Destroy(t)

	deployment.RegisterUser(t, "hs1", "alice", "password", false)

	// Create a first client, which allocates a device ID.
	c1 := deployment.Client(t, "hs1", "")
	c1.UserID, c1.AccessToken, c1.DeviceID = c1.LoginUser(t, "alice", "password")

	// Create a room where we can send events.
	roomID := c1.CreateRoom(t, map[string]interface{}{})

	// Let's send an event, and wait for it to appear in the timeline.
	eventID := c1.SendEventUnsynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "first",
		},
	})

	// When syncing, we should find the event and it should have a transaction ID on the first client.
	c1.MustSyncUntil(t, client.SyncReq{}, mustHaveTransactionID(t, roomID, eventID))

	// Create a second client, inheriting the first device ID.
	c2 := deployment.Client(t, "hs1", "")
	c2.UserID, c2.AccessToken = c2.LoginUserWithDeviceID(t, "alice", "password", c1.DeviceID)
	c2.DeviceID = c1.DeviceID

	// When syncing, we should find the event and it should *not* have a transaction ID on the second client.
	c2.MustSyncUntil(t, client.SyncReq{}, mustNotHaveTransactionID(t, roomID, eventID))
}

// TestTxnIdempotencyScopedToClientSession tests that transaction IDs are scoped to a "client session"
// and behave as expected across multiple clients even if they use the same device ID
func TestTxnIdempotencyScopedToClientSession(t *testing.T) {
	// Conduit scope transaction IDs to the device ID, not the client session.
	runtime.SkipIf(t, runtime.Conduit)

	deployment := Deploy(t, b.BlueprintCleanHS)
	defer deployment.Destroy(t)

	deployment.RegisterUser(t, "hs1", "alice", "password", false)

	// Create a first client, which allocates a device ID.
	c1 := deployment.Client(t, "hs1", "")
	c1.UserID, c1.AccessToken, c1.DeviceID = c1.LoginUser(t, "alice", "password")

	// Create a room where we can send events.
	roomID := c1.CreateRoom(t, map[string]interface{}{})

	txnId := 1
	event := b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "foo",
		},
	}
	// send an event with set txnId
	eventID1 := c1.SendEventUnsyncedWithTxnID(t, roomID, event, txnId)

	// Create a second client, inheriting the first device ID.
	c2 := deployment.Client(t, "hs1", "")
	c2.UserID, c2.AccessToken = c2.LoginUserWithDeviceID(t, "alice", "password", c1.DeviceID)
	c2.DeviceID = c1.DeviceID

	// send another event with the same txnId
	eventID2 := c2.SendEventUnsyncedWithTxnID(t, roomID, event, txnId)

	// the two events should have different event IDs as they came from different clients
	if eventID1 == eventID2 {
		t.Fatalf("Expected event IDs to be different from two clients sharing the same device ID")
	}
}

// TestTxnIdempotency tests that PUT requests idempotency follows required semantics
func TestTxnIdempotency(t *testing.T) {
	deployment := Deploy(t, b.BlueprintCleanHS)
	defer deployment.Destroy(t)

	deployment.RegisterUser(t, "hs1", "alice", "password", false)

	// Create a first client, which allocates a device ID.
	c1 := deployment.Client(t, "hs1", "")
	c1.UserID, c1.AccessToken, c1.DeviceID = c1.LoginUser(t, "alice", "password")

	// Create a room where we can send events.
	roomID1 := c1.CreateRoom(t, map[string]interface{}{})
	roomID2 := c1.CreateRoom(t, map[string]interface{}{})

	// choose a transaction ID
	txnId := 1
	event1 := b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "first",
		},
	}
	event2 := b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "second",
		},
	}

	// we send the event and get an event ID back
	eventID1 := c1.SendEventUnsyncedWithTxnID(t, roomID1, event1, txnId)

	// we send the identical event again and should get back the same event ID
	eventID2 := c1.SendEventUnsyncedWithTxnID(t, roomID1, event1, txnId)

	if eventID1 != eventID2 {
		t.Fatalf("Expected event IDs to be the same, but they were not")
	}

	// even if we change the content we should still get back the same event ID as transaction ID is the same
	eventID3 := c1.SendEventUnsyncedWithTxnID(t, roomID1, event2, txnId)

	if eventID1 != eventID3 {
		t.Fatalf("Expected event IDs to be the same even with different content, but they were not")
	}

	// if we change the room ID we should be able to use the same transaction ID
	eventID4 := c1.SendEventUnsyncedWithTxnID(t, roomID2, event1, txnId)

	if eventID4 == eventID3 {
		t.Fatalf("Expected event IDs to be the different, but they were not")
	}
}
