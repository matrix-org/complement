package csapi_tests

import (
	"fmt"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/runtime"
	"github.com/matrix-org/gomatrixserverlib/spec"
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

// Disables push rules that are introduced in MSC4306 (if present),
// because they interfere with the normal semantics of notifications in threads.
func disableMsc4306PushRules(t *testing.T, user *client.CSAPI) {
	rules := []string{".io.element.msc4306.rule.subscribed_thread", ".io.element.msc4306.rule.unsubscribed_thread"}
	for _, rule := range rules {
		res := user.GetPushRule(t, "global", "postcontent", rule)
		if res.StatusCode == 404 {
			// No push rule to disable
			continue
		}

		user.MustDisablePushRule(t, "global", "postcontent", rule)
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
// A<--B<--C<--E  [m.thread to A]
// ^
// |
// +---D          [main timeline]
// |
// +<--F          [m.reference to A]
//
//	|
//	+<--G      [m.annotation to F]
//
// Where C and D generate highlight notifications.
//
// Notification counts and receipts are handled by bob.
func TestThreadedReceipts(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // not supported
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	// Create a room with alice and bob.
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	disableMsc4306PushRules(t, alice)
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	disableMsc4306PushRules(t, bob)

	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.MustJoinRoom(t, roomID, nil)

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

	// Send another event in the thread created above.
	alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "End thread",
			"m.relates_to": map[string]interface{}{
				"event_id": eventA,
				"rel_type": "m.thread",
			},
		},
	})

	// Create another event related to the root event via a reference (i.e. but
	// not via a thread). Then create an event which is an annotation to it.
	eventF := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Reference!",
			"m.relates_to": map[string]interface{}{
				"event_id": eventA,
				"rel_type": "m.reference",
			},
		},
	})
	eventG := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.reaction",
		Content: map[string]interface{}{
			"m.relates_to": map[string]interface{}{
				"event_id": eventF,
				"rel_type": "m.annotation",
				"key":      "test",
			},
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
			return r.Get("highlight_count").Num == 2 && r.Get("notification_count").Num == 6 && !t.Exists()
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
			return r.Get("highlight_count").Num == 1 && r.Get("notification_count").Num == 3 &&
				threadNotifications.Get("highlight_count").Num == 1 && threadNotifications.Get("notification_count").Num == 3
		}),
	)

	// Mark the first event as read with a threaded receipt. This causes only the
	// notification from that event to be marked as read and only impacts the main
	// timeline.
	bob.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "receipt", "m.read", eventA}, client.WithJSONBody(t, map[string]interface{}{"thread_id": "main"}))
	bob.MustSyncUntil(
		t, client.SyncReq{Since: bobNextBatch},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == eventD
		}),
		syncHasThreadedReadReceipt(roomID, bob.UserID, eventA, "main"),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			return r.Get("highlight_count").Num == 2 && r.Get("notification_count").Num == 5 && !t.Exists()
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
			return r.Get("highlight_count").Num == 1 && r.Get("notification_count").Num == 2 &&
				threadNotifications.Get("highlight_count").Num == 1 && threadNotifications.Get("notification_count").Num == 3
		}),
	)

	// Mark the first thread event as read. This causes only the notification from
	// that event to be marked as read and only impacts the thread timeline.
	bob.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "receipt", "m.read", eventB}, client.WithJSONBody(t, map[string]interface{}{"thread_id": eventA}))
	bob.MustSyncUntil(
		t, client.SyncReq{Since: bobNextBatch},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == eventD
		}),
		syncHasThreadedReadReceipt(roomID, bob.UserID, eventA, "main"),
		syncHasThreadedReadReceipt(roomID, bob.UserID, eventB, eventA),
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
		syncHasThreadedReadReceipt(roomID, bob.UserID, eventA, "main"),
		syncHasThreadedReadReceipt(roomID, bob.UserID, eventB, eventA),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			threadNotifications := t.Get(client.GjsonEscape(eventA))
			return r.Get("highlight_count").Num == 1 && r.Get("notification_count").Num == 2 &&
				threadNotifications.Get("highlight_count").Num == 1 && threadNotifications.Get("notification_count").Num == 2
		}),
	)

	// Use an unthreaded receipt to mark the second thread event and an unthreaded event as read.
	bob.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "receipt", "m.read", eventD}, client.WithJSONBody(t, struct{}{}))
	bob.MustSyncUntil(
		t, client.SyncReq{Since: bobNextBatch},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == eventD
		}),
		syncHasUnthreadedReadReceipt(roomID, bob.UserID, eventD),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			return r.Get("highlight_count").Num == 0 && r.Get("notification_count").Num == 2 && !t.Exists()
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
			threadNotifications := t.Get(client.GjsonEscape(eventA))
			return r.Get("highlight_count").Num == 0 && r.Get("notification_count").Num == 1 &&
				threadNotifications.Get("highlight_count").Num == 0 && threadNotifications.Get("notification_count").Num == 1
		}),
	)

	// Finally, mark the entire thread as read, using the annotation.
	//
	// Note that this will *not* affect the main timeline.
	bob.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "receipt", "m.read", eventG}, client.WithJSONBody(t, map[string]interface{}{"thread_id": eventA}))
	bob.MustSyncUntil(
		t, client.SyncReq{Since: bobNextBatch},
		client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == eventD
		}),
		syncHasUnthreadedReadReceipt(roomID, bob.UserID, eventD),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			return r.Get("highlight_count").Num == 0 && r.Get("notification_count").Num == 1 && !t.Exists()
		}),
	)
	bob.MustSyncUntil(
		t,
		client.SyncReq{Since: bobNextBatch, Filter: threadFilter}, client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
			return r.Get("event_id").Str == eventD
		}),
		syncHasUnthreadedReadReceipt(roomID, bob.UserID, eventD),
		syncHasUnreadNotifs(roomID, func(r gjson.Result, t gjson.Result) bool {
			return r.Get("highlight_count").Num == 0 && r.Get("notification_count").Num == 1 && !t.Exists()
		}),
	)
}

// Regression test for https://github.com/matrix-org/matrix-spec/issues/1727 (and
// https://github.com/element-hq/synapse/pull/19838).
//
// Servers should always prefer the unthreaded receipt when there is a clash of
// receipts for the same event, and that preference must be *durable*: it must
// hold even when the unthreaded and threaded receipts are served in separate
// `/sync` responses. This happens when the two land at different stream
// positions, e.g. when they arrive over federation as separate EDUs. A
// non-durable server that only dedupes at read-time will serve the threaded
// receipt on its own in a later `/sync`, letting it incorrectly win.
//
// To test this deterministically (rather than relying on the receipts happening
// to be served in the same `/sync` response), each user is made to observe the
// unthreaded receipt down `/sync` *before* the clashing threaded receipt is
// sent, so the two are guaranteed to fall in different sync windows. A marker
// event sent after the threaded receipt then gives a deterministic point by
// which the threaded receipt must have been delivered if it was going to be. We
// assert it never is, both for the local user and for a remote user over
// federation.
func TestThreadReceiptsInSyncMSC4102(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // not supported
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	// Create a room with alice and bob.
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	disableMsc4306PushRules(t, alice)
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})
	disableMsc4306PushRules(t, bob)
	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.MustJoinRoom(t, roomID, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
	})
	eventA := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world!",
		},
	})
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

	// Send an unthreaded RR for event B, and wait until both alice and bob have
	// seen it down /sync. This pins the unthreaded receipt to its own stream
	// position / sync window on each server, so the clashing threaded receipt
	// sent next is forced into a *separate* sync response.
	alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "receipt", "m.read", eventB}, client.WithJSONBody(t, struct{}{}))
	aliceNextBatch := alice.MustSyncUntil(
		t,
		client.SyncReq{},
		syncHasUnthreadedReadReceipt(roomID, alice.UserID, eventB),
	)
	bobNextBatch := bob.MustSyncUntil(
		t,
		client.SyncReq{},
		syncHasUnthreadedReadReceipt(roomID, alice.UserID, eventB),
	)

	// Now send a clashing threaded RR for the *same* event. Pre-fix this was
	// inserted at a later stream position (and federated as a separate EDU) and
	// so could be served on its own in a later /sync, incorrectly winning.
	alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "receipt", "m.read", eventB}, client.WithJSONBody(t, map[string]interface{}{"thread_id": eventA}))

	// Send a marker event strictly after the clashing receipt. Once a server has
	// delivered this event down /sync, the (earlier) threaded receipt must also
	// have been processed and delivered if it was going to be.
	eventC := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Marker",
		},
	})

	// The threaded receipt must never win, neither locally nor over federation.
	assertUnthreadedReceiptWins(t, alice, roomID, aliceNextBatch, alice.UserID, eventB, eventA, eventC)
	assertUnthreadedReceiptWins(t, bob, roomID, bobNextBatch, alice.UserID, eventB, eventA, eventC)
}

// assertUnthreadedReceiptWins drives an incremental /sync for `user` from
// `since` until it observes `markerEventID` in the room timeline, and fails the
// test if at any point along the way a threaded read receipt for `clashEventID`
// (in thread `clashThreadID`) from `receiptUserID` was served. The marker event
// must have been sent *after* the clashing threaded receipt, so that observing
// it guarantees the receipt has already been delivered if it was going to be.
func assertUnthreadedReceiptWins(t *testing.T, user *client.CSAPI, roomID, since, receiptUserID, clashEventID, clashThreadID, markerEventID string) {
	t.Helper()
	sawThreadedClash := false
	// We can't use a `syncHasThreadedReadReceipt` check for this: MustSyncUntil
	// drops a check once it passes, whereas we need to keep scanning every
	// response for the forbidden receipt right up until the marker arrives. So
	// we accumulate a flag by hand and use the marker as the (positive) stop
	// condition.
	marker := client.SyncTimelineHasEventID(roomID, markerEventID)
	user.MustSyncUntil(t, client.SyncReq{Since: since}, func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		ephemeral := topLevelSyncJSON.Get("rooms.join." + client.GjsonEscape(roomID) + ".ephemeral.events")
		for _, ev := range ephemeral.Array() {
			if ev.Get("type").Str != "m.receipt" {
				continue
			}
			receipt := ev.Get("content").Get(clashEventID).Get(`m\.read`).Get(receiptUserID)
			if receipt.Exists() && receipt.Get("thread_id").Str == clashThreadID {
				sawThreadedClash = true
			}
		}
		// Wait until the marker event arrives, by which point any receipt for the
		// clashing event must already have been delivered.
		return marker(clientUserID, topLevelSyncJSON)
	})
	if sawThreadedClash {
		t.Fatalf(
			"%s saw a threaded read receipt for %s (thread %s) win down /sync; the clashing unthreaded receipt should always win (MSC4102)",
			user.UserID, clashEventID, clashThreadID,
		)
	}
}
