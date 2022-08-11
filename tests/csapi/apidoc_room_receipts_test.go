package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/tidwall/gjson"
)

// tests/10apidoc/37room-receipts.pl

func createRoomForReadReceipts(t *testing.T, c *client.CSAPI, deployment *docker.Deployment) (string, string) {
	roomID := c.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})

	c.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(c.UserID, roomID))

	eventID := c.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world!",
		},
	})

	return roomID, eventID
}

func syncHasReadReceipt(roomID, userID, eventID string) client.SyncCheckOpt {
	return client.SyncEphemeralHas(roomID, func(result gjson.Result) bool {
		return result.Get("type").Str == "m.receipt" &&
			result.Get("content").Get(eventID).Get(`m\.read`).Get(userID).Exists()
	})
}

// sytest: POST /rooms/:room_id/receipt can create receipts
func TestRoomReceipts(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	roomID, eventID := createRoomForReadReceipts(t, alice, deployment)

	alice.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "receipt", "m.read", eventID}, client.WithJSONBody(t, struct{}{}))

	// Make sure the read receipt shows up in sync.
	alice.MustSyncUntil(t, client.SyncReq{}, syncHasReadReceipt(roomID, alice.UserID, eventID))
}

// sytest: POST /rooms/:room_id/read_markers can create read marker
func TestRoomReadMarkers(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	roomID, eventID := createRoomForReadReceipts(t, alice, deployment)

	reqBody := client.WithJSONBody(t, map[string]interface{}{
		"m.fully_read": eventID,
		"m.read":       eventID,
	})
	alice.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "read_markers"}, reqBody)

	// Make sure the read receipt shows up in sync.
	alice.MustSyncUntil(t, client.SyncReq{}, syncHasReadReceipt(roomID, alice.UserID, eventID))

	// Make sure that the fully_read receipt shows up in account data via sync.
	// Use the same token as above to replay the syncs.
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncRoomAccountDataHas(roomID, func(result gjson.Result) bool {
		return result.Get("type").Str == "m.fully_read" &&
			result.Get("content.event_id").Str == eventID
	}))
}
