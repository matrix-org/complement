//go:build msc3970
// +build msc3970

// This files contains tests for MSC3970, which changes the scope
// of transaction IDs to be per-device instead of per-access token.
// https://github.com/matrix-org/matrix-spec-proposals/pull/3970

package tests

import (
	"fmt"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
)

func mustHaveTransactionID(t *testing.T, roomID, eventID, expectedTxnId string) client.SyncCheckOpt {
	return client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		if r.Get("event_id").Str == eventID {
			unsignedTxnId := r.Get("unsigned.transaction_id")
			if !unsignedTxnId.Exists() {
				t.Fatalf("Event %s in room %s should have a 'unsigned.transaction_id', but it did not", eventID, roomID)
			}

			must.EqualStr(t, unsignedTxnId.Str, expectedTxnId, fmt.Sprintf("Event %s in room %s should have a 'unsigned.transaction_id'", eventID, roomID))

			return true
		}

		return false
	})
}

// TestTxnScopeOnLocalEchoMSC3970 checks that the transaction IDs are scoped to the device,
// and not just the access token, as per MSC3970.
func TestTxnScopeOnLocalEchoMSC3970(t *testing.T) {
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
	c2.MustSyncUntil(t, client.SyncReq{}, mustHaveTransactionID(t, roomID, eventID, txnId))
}

// TestTxnIdempotencyScopedToClientDeviceMSC3970 tests that transaction IDs are scoped to a device
func TestTxnIdempotencyScopedToDeviceMSC3970(t *testing.T) {
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
	must.EqualStr(t, eventID2, eventID1, "Expected eventID1 and eventID2 to be equal from two clients sharing the same device ID")
}
