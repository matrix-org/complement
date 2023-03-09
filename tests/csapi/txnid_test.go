package csapi_tests

import (
	"fmt"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/must"
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

	txnId := "abcdefg"
	// Let's send an event, and wait for it to appear in the timeline.
	eventID := c.SendEventUnsyncedWithTxnID(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "first",
		},
	}, txnId)

	// The transaction ID should be present on the GET /rooms/{roomID}/event/{eventID} response.
	res := c.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "event", eventID})
	body := client.ParseJSON(t, res)
	result := gjson.ParseBytes(body)
	unsignedTxnId := result.Get("unsigned.transaction_id")
	if !unsignedTxnId.Exists() {
		t.Fatalf("Event did not have a 'unsigned.transaction_id' on the GET /rooms/%s/event/%s response", roomID, eventID)
	}

	must.EqualStr(t, unsignedTxnId.Str, txnId, fmt.Sprintf("Event had an incorrect 'unsigned.transaction_id' on GET /rooms/%s/event/%s response", eventID, roomID))
}


func mustHaveTransactionIDForEvent(t *testing.T, roomID, eventID, expectedTxnId string) client.SyncCheckOpt {
	return client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		if r.Get("event_id").Str == eventID {
			unsignedTxnId := r.Get("unsigned.transaction_id")
			if !unsignedTxnId.Exists() {
				t.Fatalf("Event %s in room %s should have a 'unsigned.transaction_id', but it did not", eventID, roomID)
			}

			must.EqualStr(t, unsignedTxnId.Str, expectedTxnId, fmt.Sprintf("Event %s in room %s had an incorrect 'unsigned.transaction_id'", eventID, roomID))

			return true
		}

		return false
	})
}

func mustNotHaveTransactionIDForEvent(t *testing.T, roomID, eventID string) client.SyncCheckOpt {
	return client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		if r.Get("event_id").Str == eventID {
			unsignedTxnId := r.Get("unsigned.transaction_id")
			if unsignedTxnId.Exists() {
				t.Fatalf("Event %s in room %s should NOT have a 'unsigned.transaction_id', but it did (%s)", eventID, roomID, unsignedTxnId.Str)
			}

			return true
		}

		return false
	})
}

// TestTxnScopeOnLocalEcho tests that transaction IDs in the sync response are scoped to the "client session", not the device
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

	txnId := "abdefgh"
	// Let's send an event, and wait for it to appear in the timeline.
	eventID := c1.SendEventUnsyncedWithTxnID(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "first",
		},
	}, txnId)

	// When syncing, we should find the event and it should have a transaction ID on the first client.
	c1.MustSyncUntil(t, client.SyncReq{}, mustHaveTransactionIDForEvent(t, roomID, eventID, txnId))

	// Create a second client, inheriting the first device ID.
	c2 := deployment.Client(t, "hs1", "")
	c2.UserID, c2.AccessToken, c2.DeviceID = c2.LoginUser(t, "alice", "password", client.WithDeviceID(c1.DeviceID))
	must.EqualStr(t, c1.DeviceID, c2.DeviceID, "Device ID should be the same")

	// When syncing, we should find the event and it should *not* have a transaction ID on the second client.
	c2.MustSyncUntil(t, client.SyncReq{}, mustNotHaveTransactionIDForEvent(t, roomID, eventID))
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

	txnId := "abcdef"
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
	c2.UserID, c2.AccessToken, c2.DeviceID = c2.LoginUser(t, "alice", "password", client.WithDeviceID(c1.DeviceID))
	must.EqualStr(t, c1.DeviceID, c2.DeviceID, "Device ID should be the same")

	// send another event with the same txnId via the second client
	eventID2 := c2.SendEventUnsyncedWithTxnID(t, roomID, event, txnId)

	// the two events should have different event IDs as they came from different clients
	must.NotEqualStr(t, eventID2, eventID1, "Expected eventID1 and eventID2 to be different from two clients sharing the same device ID")
}

// TestTxnIdempotency tests that PUT requests idempotency follows required semantics
func TestTxnIdempotency(t *testing.T) {
	// Conduit appears to be tracking transaction IDs individually rather than combined with the request URI/room ID
	runtime.SkipIf(t, runtime.Conduit)

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
	txnId := "abc"
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

	must.EqualStr(t, eventID2, eventID1, "Expected eventID1 and eventID2 to be the same, but they were not")

	// even if we change the content we should still get back the same event ID as transaction ID is the same
	eventID3 := c1.SendEventUnsyncedWithTxnID(t, roomID1, event2, txnId)

	must.EqualStr(t, eventID3, eventID1, "Expected eventID3 and eventID2 to be the same even with different content, but they were not")

	// if we change the room ID we should be able to use the same transaction ID
	eventID4 := c1.SendEventUnsyncedWithTxnID(t, roomID2, event1, txnId)

	must.NotEqualStr(t, eventID4, eventID3, "Expected eventID4 and eventID3 to be different, but they were not")
}


func mustHaveTransactionID(t *testing.T, roomID, eventID, expectedTxnId string) client.SyncCheckOpt {
	return client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		if r.Get("event_id").Str == eventID {
			if !r.Get("unsigned.transaction_id").Exists() {
				t.Fatalf("Event %s in room %s should have a 'unsigned.transaction_id', but it did not", eventID, roomID)
			}

			txnIdFromSync := r.Get("unsigned.transaction_id").Str

			if txnIdFromSync != expectedTxnId {
				t.Fatalf("Event %s in room %s should have a 'unsigned.transaction_id' of %s but found %s", eventID, roomID, expectedTxnId, txnIdFromSync)
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
				t.Fatalf("Event %s in room %s should NOT have a 'unsigned.transaction_id', but it did (%s)", eventID, roomID, res.Str)
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

	txnId := "abdefgh"
	// Let's send an event, and wait for it to appear in the timeline.
	eventID := c1.SendEventUnsyncedWithTxnID(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "first",
		},
	}, txnId)

	// When syncing, we should find the event and it should have a transaction ID on the first client.
	c1.MustSyncUntil(t, client.SyncReq{}, mustHaveTransactionID(t, roomID, eventID, txnId))

	// Create a second client, inheriting the first device ID.
	c2 := deployment.Client(t, "hs1", "")
	c2.UserID, c2.AccessToken, _ = c2.LoginUser(t, "alice", "password", client.WithDeviceID(c1.DeviceID))
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

	txnId := "abcdef"
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
	c2.UserID, c2.AccessToken, _ = c2.LoginUser(t, "alice", "password", client.WithDeviceID(c1.DeviceID))
	c2.DeviceID = c1.DeviceID

	// send another event with the same txnId
	eventID2 := c2.SendEventUnsyncedWithTxnID(t, roomID, event, txnId)

	// the two events should have different event IDs as they came from different clients
	must.NotEqualStr(t, eventID2, eventID1, "Expected eventID1 and eventID2 to be different from two clients sharing the same device ID")
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
	txnId := "abc"
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

	must.EqualStr(t, eventID2, eventID1, "Expected eventID1 and eventID2 to be the same, but they were not")

	// even if we change the content we should still get back the same event ID as transaction ID is the same
	eventID3 := c1.SendEventUnsyncedWithTxnID(t, roomID1, event2, txnId)

	must.EqualStr(t, eventID3, eventID1, "Expected eventID3 and eventID2 to be the same even with different content, but they were not")

	// if we change the room ID we should be able to use the same transaction ID
	eventID4 := c1.SendEventUnsyncedWithTxnID(t, roomID2, event1, txnId)

	must.NotEqualStr(t, eventID4, eventID3, "Expected eventID4 and eventID3 to be different, but they were not")
}
