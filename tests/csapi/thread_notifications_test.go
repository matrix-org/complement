package csapi_tests

import (
	"fmt"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
)

func syncHasUnreadNotifs(roomID string, check func(gjson.Result) bool) client.SyncCheckOpt {
	return func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		unreadNotifications := topLevelSyncJSON.Get("rooms.join." + client.GjsonEscape(roomID) + ".unread_notifications")
		if !unreadNotifications.Exists() {
			return fmt.Errorf("syncHasUnreadNotifs(%s): missing notifications", roomID)
		}
		if check(unreadNotifications) {
			return nil
		}
		return fmt.Errorf("syncHasUnreadNotifs(%s): check function did not pass: %v", roomID, unreadNotifications.Raw)
	}
}

// Test behavior of threaded receipts and notifications.
//
// Send a series of messages, some of which are in threads. Send combinations of
// threaded and unthreaded receipts and ensure the notification counts are updated
// appropriately.
func TestThreadedReceipts(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	// Create a room with alice and bob.
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.JoinRoom(t, roomID, nil)

	token := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	// Send messages as alice and then check the highlight and notification counts from bob.
	firstEventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world!",
		},
	})
	bob.MustSyncUntil(
		t, client.SyncReq{Since: token},
    client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == firstEventID
		}),
		syncHasUnreadNotifs(roomID, func(r gjson.Result) bool {
			return r.Get("highlight_count").Num == 0 && r.Get("notification_count").Num == 1
		}),
	)

	// Send a highlight message and re-check counts.
	secondEventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    fmt.Sprintf("Hello %s!", bob.UserID),
		},
	})
	bob.MustSyncUntil(
		t, client.SyncReq{Since: token},
    client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == secondEventID
		}),
		syncHasUnreadNotifs(roomID, func(r gjson.Result) bool {
			return r.Get("highlight_count").Num == 1 && r.Get("notification_count").Num == 2
		}),
	)

	// Mark the first event as read and check counts.
	bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "receipt", "m.read", firstEventID}, client.WithJSONBody(t, struct{}{}))
	bob.MustSyncUntil(
		t, client.SyncReq{Since: token},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == secondEventID
		}),
		syncHasUnreadNotifs(roomID, func(r gjson.Result) bool {
			return r.Get("highlight_count").Num == 1 && r.Get("notification_count").Num == 1
		}),
	)

	// Mark the second event as read and check counts.
	bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "receipt", "m.read", secondEventID}, client.WithJSONBody(t, struct{}{}))
	bob.MustSyncUntil(
		t, client.SyncReq{Since: token},
    client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == secondEventID
		}),
		syncHasUnreadNotifs(roomID, func(r gjson.Result) bool {
			return r.Get("highlight_count").Num == 0 && r.Get("notification_count").Num == 0
		}),
	)
}
