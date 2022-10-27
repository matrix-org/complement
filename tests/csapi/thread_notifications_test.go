package csapi_tests

import (
	"fmt"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/runtime"
)

func syncHasUnreadNotifs(roomID string, check func(gjson.Result, gjson.Result) bool) client.SyncCheckOpt {
	return func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		unreadNotifications := topLevelSyncJSON.Get("rooms.join." + client.GjsonEscape(roomID) + ".unread_notifications")
		unreadThreadNotifications := topLevelSyncJSON.Get("rooms.join." + client.GjsonEscape(roomID) + ".unread_thread_notifications")
		if !unreadNotifications.Exists() {
			return fmt.Errorf("syncHasUnreadNotifs(%s): missing notifications", roomID)
		}
		if check(unreadNotifications, unreadThreadNotifications) {
			return nil
		}
		return fmt.Errorf("syncHasUnreadNotifs(%s): check function did not pass: %v / %v", roomID, unreadNotifications.Raw, unreadThreadNotifications.Raw)
	}
}

// Test behavior of threaded receipts and notifications.
//
// 1. Send a series of messages, some of which are in threads.
// 2. Send combinations of threaded and unthreaded receipts.
// 3. Ensure the notification counts are updated appropriately.
//
// This sends four messages as alice creating a timeline like:
//
// A<--B<--C    [thread]
// ^
// +---D        [main timeline]
//
// Where C and D generate highlight notifications.
//
// Notification counts and receipts are handled by bob.
func TestThreadedReceipts(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // not supported
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	// Create a room with alice and bob.
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.JoinRoom(t, roomID, nil)

	token := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	// Send an initial message as alice.
	firstEventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world!",
		},
	})

	// Create a thread from the above message and send both two messages in it,
	// the second of which is a mention (causing a highlight).
	firstThreadEventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Start thread!",
			"m.relates_to": map[string]interface{}{
				"event_id": firstEventID,
				"rel_type": "m.thread",
			},
		},
	})
	alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    fmt.Sprintf("Thread response %s!", bob.UserID),
			"m.relates_to": map[string]interface{}{
				"event_id": firstEventID,
				"rel_type": "m.thread",
			},
		},
	})

	// Send an additional unthreaded message, which is a mention (causing a highlight).
	secondEventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    fmt.Sprintf("Hello %s!", bob.UserID),
		},
	})

	// A filter to get thread notifications.
	threadFilter := `{"room":{"timeline":{"unread_thread_notifications":true}}}`

	// Check the unthreaded and threaded counts, which should include all previously
	// sent messages.
	bob.MustSyncUntil(
		t, client.SyncReq{Since: token},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == secondEventID
		}),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			return r.Get("highlight_count").Num == 2 && r.Get("notification_count").Num == 4 && !t.Exists()
		}),
	)
	bob.MustSyncUntil(
		t,
		client.SyncReq{Since: token, Filter: threadFilter},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == secondEventID
		}),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			threadNotifications := t.Get(client.GjsonEscape(firstEventID))
			return r.Get("highlight_count").Num == 1 && r.Get("notification_count").Num == 2 &&
			  threadNotifications.Get("highlight_count").Num == 1 && threadNotifications.Get("notification_count").Num == 2
		}),
	)

	// Mark the first event as read with a threaded receipt. This causes only the
	// notification from that event to be marked as read and only impacts the main
	// timeline.
	bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "receipt", "m.read", firstEventID}, client.WithJSONBody(t, map[string]interface{}{"thread_id": "main"}))
	bob.MustSyncUntil(
		t, client.SyncReq{Since: token},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == secondEventID
		}),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			return r.Get("highlight_count").Num == 2 && r.Get("notification_count").Num == 3 && !t.Exists()
		}),
	)
	bob.MustSyncUntil(
		t,
		client.SyncReq{Since: token, Filter: threadFilter},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == secondEventID
		}),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			threadNotifications := t.Get(client.GjsonEscape(firstEventID))
			return r.Get("highlight_count").Num == 1 && r.Get("notification_count").Num == 1 &&
			  threadNotifications.Get("highlight_count").Num == 1 && threadNotifications.Get("notification_count").Num == 2
		}),
	)

	// Mark the first thread event as read. This causes only the notification from
	// that event to be marked as read and only impacts the thread timeline.
	bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "receipt", "m.read", firstThreadEventID}, client.WithJSONBody(t, map[string]interface{}{"thread_id": firstEventID}))
	bob.MustSyncUntil(
		t, client.SyncReq{Since: token},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == secondEventID
		}),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			return r.Get("highlight_count").Num == 2 && r.Get("notification_count").Num == 2 && !t.Exists()
		}),
	)
	bob.MustSyncUntil(
		t,
		client.SyncReq{Since: token, Filter: threadFilter},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == secondEventID
		}),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			threadNotifications := t.Get(client.GjsonEscape(firstEventID))
			return r.Get("highlight_count").Num == 1 && r.Get("notification_count").Num == 1 &&
			  threadNotifications.Get("highlight_count").Num == 1 && threadNotifications.Get("notification_count").Num == 1
		}),
	)

	// Mark the entire room as read by sending an unthreaded read receipt on the last
	// event. This clears all notification counts.
	bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "receipt", "m.read", secondEventID}, client.WithJSONBody(t, struct{}{}))
	bob.MustSyncUntil(
		t, client.SyncReq{Since: token},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == secondEventID
		}),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			return r.Get("highlight_count").Num == 0 && r.Get("notification_count").Num == 0 && !t.Exists()
		}),
	)
	bob.MustSyncUntil(
		t,
		client.SyncReq{Since: token, Filter: threadFilter},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == secondEventID
		}),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			return r.Get("highlight_count").Num == 0 && r.Get("notification_count").Num == 0 && !t.Exists()
		}),
	)
}
