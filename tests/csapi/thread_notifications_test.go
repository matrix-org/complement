package csapi_tests

import (
	"fmt"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/runtime"
)

// Builds a `SyncCheckOpt` which enforces that a sync result satisfies some `check` function
// on the `unread_notifications` and `unread_thread_notifications` fields for the given room.
// The `unread_notifications` field must exist, or else the overall SyncCheckOpt will be considered
// as failing.
func syncHasUnreadNotifs(roomID string, check func(gjson.Result, gjson.Result) bool) client.SyncCheckOpt {
	return func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		unreadNotifications := topLevelSyncJSON.Get("rooms.join." + client.GjsonEscape(roomID) + ".unread_notifications")
		unreadThreadNotifications := topLevelSyncJSON.Get("rooms.join." + client.GjsonEscape(roomID) + ".unread_thread_notifications")
		if !unreadNotifications.Exists() {
			return fmt.Errorf("syncHasUnreadNotifs(%s): missing unread notifications", roomID)
		}
		if check(unreadNotifications, unreadThreadNotifications) {
			return nil
		}
		return fmt.Errorf("syncHasUnreadNotifs(%s): check function did not pass: %v / %v", roomID, unreadNotifications.Raw, unreadThreadNotifications.Raw)
	}
}

// Builds a `SyncCheckOpt` which enforces that a sync result has an unthreaded read
// receipt for the given user in the the given room at the expected event.
func syncHasUnthreadedReadReceipt(roomID, userID, eventID string) client.SyncCheckOpt {
	return client.SyncEphemeralHas(roomID, func(result gjson.Result) bool {
		userReceipt := result.Get("content").Get(eventID).Get(`m\.read`).Get(userID)
		return result.Get("type").Str == "m.receipt" && userReceipt.Exists() && !userReceipt.Get("thread_id").Exists()
	})
}

// Builds a `SyncCheckOpt` which enforces that a sync result has an threaded read
// receipt for the given user in the the given room at the expected event for the
// given thread.
func syncHasThreadedReadReceipt(roomID, userID, eventID, threadID string) client.SyncCheckOpt {
	return client.SyncEphemeralHas(roomID, func(result gjson.Result) bool {
		userReceipt := result.Get("content").Get(eventID).Get(`m\.read`).Get(userID)
		return result.Get("type").Str == "m.receipt" && userReceipt.Exists() && userReceipt.Get("thread_id").Str == threadID
	})
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

	// A next batch token which is past the initial room creation.
	bobNextBatch := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	// Send an initial message as alice.
	eventA := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world!",
		},
	})

	// Create a thread from the above message and send both two messages in it,
	// the second of which is a mention (causing a highlight).
	eventB := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Start thread!",
			"m.relates_to": map[string]interface{}{
				"event_id": eventA,
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
				"event_id": eventA,
				"rel_type": "m.thread",
			},
		},
	})

	// Send an additional unthreaded message, which is a mention (causing a highlight).
	eventD := alice.SendEventSynced(t, roomID, b.Event{
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
		t, client.SyncReq{Since: bobNextBatch},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == eventD
		}),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			return r.Get("highlight_count").Num == 2 && r.Get("notification_count").Num == 4 && !t.Exists()
		}),
	)
	bob.MustSyncUntil(
		t,
		client.SyncReq{Since: bobNextBatch, Filter: threadFilter},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == eventD
		}),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			threadNotifications := t.Get(client.GjsonEscape(eventA))
			return r.Get("highlight_count").Num == 1 && r.Get("notification_count").Num == 2 &&
			  threadNotifications.Get("highlight_count").Num == 1 && threadNotifications.Get("notification_count").Num == 2
		}),
	)

	// Mark the first event as read with a threaded receipt. This causes only the
	// notification from that event to be marked as read and only impacts the main
	// timeline.
	bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "receipt", "m.read", eventA}, client.WithJSONBody(t, map[string]interface{}{"thread_id": "main"}))
	bob.MustSyncUntil(
		t, client.SyncReq{Since: bobNextBatch},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == eventD
		}),
		syncHasThreadedReadReceipt(roomID, bob.UserID, eventA, "main"),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			return r.Get("highlight_count").Num == 2 && r.Get("notification_count").Num == 3 && !t.Exists()
		}),
	)
	bob.MustSyncUntil(
		t,
		client.SyncReq{Since: bobNextBatch, Filter: threadFilter},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == eventD
		}),
		syncHasThreadedReadReceipt(roomID, bob.UserID, eventA, "main"),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			threadNotifications := t.Get(client.GjsonEscape(eventA))
			return r.Get("highlight_count").Num == 1 && r.Get("notification_count").Num == 1 &&
			  threadNotifications.Get("highlight_count").Num == 1 && threadNotifications.Get("notification_count").Num == 2
		}),
	)

	// Mark the first thread event as read. This causes only the notification from
	// that event to be marked as read and only impacts the thread timeline.
	bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "receipt", "m.read", eventB}, client.WithJSONBody(t, map[string]interface{}{"thread_id": eventA}))
	bob.MustSyncUntil(
		t, client.SyncReq{Since: bobNextBatch},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == eventD
		}),
		syncHasThreadedReadReceipt(roomID, bob.UserID, eventA, "main"),
		syncHasThreadedReadReceipt(roomID, bob.UserID, eventB, eventA),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			return r.Get("highlight_count").Num == 2 && r.Get("notification_count").Num == 2 && !t.Exists()
		}),
	)
	bob.MustSyncUntil(
		t,
		client.SyncReq{Since: bobNextBatch, Filter: threadFilter},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == eventD
		}),
		syncHasThreadedReadReceipt(roomID, bob.UserID, eventA, "main"),
		syncHasThreadedReadReceipt(roomID, bob.UserID, eventB, eventA),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			threadNotifications := t.Get(client.GjsonEscape(eventA))
			return r.Get("highlight_count").Num == 1 && r.Get("notification_count").Num == 1 &&
			  threadNotifications.Get("highlight_count").Num == 1 && threadNotifications.Get("notification_count").Num == 1
		}),
	)

	// Mark the entire room as read by sending an unthreaded read receipt on the last
	// event. This clears all notification counts.
	bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "receipt", "m.read", eventD}, client.WithJSONBody(t, struct{}{}))
	bob.MustSyncUntil(
		t, client.SyncReq{Since: bobNextBatch},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == eventD
		}),
		syncHasUnthreadedReadReceipt(roomID, bob.UserID, eventD),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			return r.Get("highlight_count").Num == 0 && r.Get("notification_count").Num == 0 && !t.Exists()
		}),
	)
	bob.MustSyncUntil(
		t,
		client.SyncReq{Since: bobNextBatch, Filter: threadFilter},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == eventD
		}),
		syncHasUnthreadedReadReceipt(roomID, bob.UserID, eventD),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			return r.Get("highlight_count").Num == 0 && r.Get("notification_count").Num == 0 && !t.Exists()
		}),
	)
}
