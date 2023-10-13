package csapi_tests

import (
	"fmt"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/tidwall/gjson"
)

// TestTxnInEvent checks that the transaction ID is present when getting the event from the /rooms/{roomID}/event/{eventID} endpoint.
func TestTxnInEvent(t *testing.T) {
	// Dendrite implementation is broken
	// See https://github.com/matrix-org/dendrite/issues/3000
	runtime.SkipIf(t, runtime.Dendrite)

	deployment := complement.Deploy(t, b.BlueprintCleanHS)
	defer deployment.Destroy(t)

	c := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
		Password:        "password",
	})

	// Create a room where we can send events.
	roomID := c.MustCreateRoom(t, map[string]interface{}{})

	txnId := "abcdefg"
	// Let's send an event, and wait for it to appear in the timeline.
	eventID := c.Unsafe_SendEventUnsyncedWithTxnID(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "first",
		},
	}, txnId)

	// The transaction ID should be present on the GET /rooms/{roomID}/event/{eventID} response.
	res := c.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "event", eventID})
	body := client.ParseJSON(t, res)
	result := gjson.ParseBytes(body)
	unsignedTxnId := result.Get("unsigned.transaction_id")
	if !unsignedTxnId.Exists() {
		t.Fatalf("Event did not have a 'unsigned.transaction_id' on the GET /rooms/%s/event/%s response", roomID, eventID)
	}

	must.Equal(t, unsignedTxnId.Str, txnId, fmt.Sprintf("Event had an incorrect 'unsigned.transaction_id' on GET /rooms/%s/event/%s response", eventID, roomID))
}

func mustHaveTransactionIDForEvent(t *testing.T, roomID, eventID, expectedTxnId string) client.SyncCheckOpt {
	return client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		if r.Get("event_id").Str == eventID {
			unsignedTxnId := r.Get("unsigned.transaction_id")
			if !unsignedTxnId.Exists() {
				t.Fatalf("Event %s in room %s should have a 'unsigned.transaction_id', but it did not", eventID, roomID)
			}

			must.Equal(t, unsignedTxnId.Str, expectedTxnId, fmt.Sprintf("Event %s in room %s had an incorrect 'unsigned.transaction_id'", eventID, roomID))

			return true
		}

		return false
	})
}

// TestTxnScopeOnLocalEcho tests that transaction IDs in the sync response are scoped to the device
func TestTxnScopeOnLocalEcho(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite)

	deployment := complement.Deploy(t, b.BlueprintCleanHS)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
		Password:        "password",
	})

	// Create a first client, which allocates a device ID.
	c1 := deployment.Client(t, "hs1", "")
	c1.UserID, c1.AccessToken, c1.DeviceID = c1.LoginUser(t, alice.UserID, "password")

	// Create a room where we can send events.
	roomID := c1.MustCreateRoom(t, map[string]interface{}{})

	txnId := "abdefgh"
	// Let's send an event, and wait for it to appear in the timeline.
	eventID := c1.Unsafe_SendEventUnsyncedWithTxnID(t, roomID, b.Event{
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
	must.Equal(t, c1.DeviceID, c2.DeviceID, "Device ID should be the same")

	// When syncing, we should find the event and it should have the same transaction ID on the second client.
	c2.MustSyncUntil(t, client.SyncReq{}, mustHaveTransactionIDForEvent(t, roomID, eventID, txnId))
}

// TestTxnIdempotencyScopedToDevice tests that transaction IDs are scoped to a device
// and behave as expected across multiple clients if they use the same device ID
func TestTxnIdempotencyScopedToDevice(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite)

	deployment := complement.Deploy(t, b.BlueprintCleanHS)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
		Password:        "password",
	})

	// Create a first client, which allocates a device ID.
	c1 := deployment.Client(t, "hs1", "")
	c1.UserID, c1.AccessToken, c1.DeviceID = c1.LoginUser(t, alice.UserID, "password")

	// Create a room where we can send events.
	roomID := c1.MustCreateRoom(t, map[string]interface{}{})

	txnId := "abcdef"
	event := b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "foo",
		},
	}
	// send an event with set txnId
	eventID1 := c1.Unsafe_SendEventUnsyncedWithTxnID(t, roomID, event, txnId)

	// Create a second client, inheriting the first device ID.
	c2 := deployment.Client(t, "hs1", "")
	c2.UserID, c2.AccessToken, c2.DeviceID = c2.LoginUser(t, "alice", "password", client.WithDeviceID(c1.DeviceID))
	must.Equal(t, c1.DeviceID, c2.DeviceID, "Device ID should be the same")

	// send another event with the same txnId via the second client
	eventID2 := c2.Unsafe_SendEventUnsyncedWithTxnID(t, roomID, event, txnId)

	// the two events should have the same event IDs as they came from the same device
	must.Equal(t, eventID2, eventID1, "Expected eventID1 and eventID2 to be the same from two clients sharing the same device ID")
}

// TestTxnIdempotency tests that PUT requests idempotency follows required semantics
func TestTxnIdempotency(t *testing.T) {
	// Conduit appears to be tracking transaction IDs individually rather than combined with the request URI/room ID
	runtime.SkipIf(t, runtime.Conduit)

	deployment := complement.Deploy(t, b.BlueprintCleanHS)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
		Password:        "password",
	})

	// Create a first client, which allocates a device ID.
	c1 := deployment.Client(t, "hs1", "")
	c1.UserID, c1.AccessToken, c1.DeviceID = c1.LoginUser(t, alice.UserID, "password")

	// Create a room where we can send events.
	roomID1 := c1.MustCreateRoom(t, map[string]interface{}{})
	roomID2 := c1.MustCreateRoom(t, map[string]interface{}{})

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
	eventID1 := c1.Unsafe_SendEventUnsyncedWithTxnID(t, roomID1, event1, txnId)

	// we send the identical event again and should get back the same event ID
	eventID2 := c1.Unsafe_SendEventUnsyncedWithTxnID(t, roomID1, event1, txnId)

	must.Equal(t, eventID2, eventID1, "Expected eventID1 and eventID2 to be the same, but they were not")

	// even if we change the content we should still get back the same event ID as transaction ID is the same
	eventID3 := c1.Unsafe_SendEventUnsyncedWithTxnID(t, roomID1, event2, txnId)

	must.Equal(t, eventID3, eventID1, "Expected eventID3 and eventID2 to be the same even with different content, but they were not")

	// if we change the room ID we should be able to use the same transaction ID
	eventID4 := c1.Unsafe_SendEventUnsyncedWithTxnID(t, roomID2, event1, txnId)

	must.NotEqual(t, eventID4, eventID3, "Expected eventID4 and eventID3 to be different, but they were not")
}

// TestTxnIdWithRefreshToken tests that when a client refreshes its access token,
// it still gets back a transaction ID in the sync response and idempotency is respected.
func TestTxnIdWithRefreshToken(t *testing.T) {
	// Dendrite and Conduit don't support refresh tokens yet.
	runtime.SkipIf(t, runtime.Dendrite, runtime.Conduit)

	deployment := complement.Deploy(t, b.BlueprintCleanHS)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
		Password:        "password",
	})
	localpart, _, err := gomatrixserverlib.SplitID('@', alice.UserID)
	must.NotError(t, "failed to get localpart from user ID", err)

	c := deployment.Client(t, "hs1", "")

	var refreshToken string
	c.UserID, c.AccessToken, refreshToken, c.DeviceID, _ = c.LoginUserWithRefreshToken(t, localpart, "password")

	// Create a room where we can send events.
	roomID := c.MustCreateRoom(t, map[string]interface{}{})

	txnId := "abcdef"
	// We send an event
	eventID1 := c.Unsafe_SendEventUnsyncedWithTxnID(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "first",
		},
	}, txnId)

	// Use the refresh token to get a new access token.
	c.AccessToken, refreshToken, _ = c.ConsumeRefreshToken(t, refreshToken)

	// When syncing, we should find the event and it should also have the correct transaction ID even
	// though the access token is different.
	c.MustSyncUntil(t, client.SyncReq{}, mustHaveTransactionIDForEvent(t, roomID, eventID1, txnId))

	// We try sending the event again with the same transaction ID
	eventID2 := c.Unsafe_SendEventUnsyncedWithTxnID(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "first",
		},
	}, txnId)

	// The event should have been deduplicated and we should get back the same event ID
	must.Equal(t, eventID2, eventID1, "Expected eventID1 and eventID2 to be the same from a client using a refresh token")
}
