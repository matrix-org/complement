// +build msc2716

// This file contains tests for incrementally importing history to an existing room,
// a currently experimental feature defined by MSC2716, which you can read here:
// https://github.com/matrix-org/matrix-doc/pull/2716

package tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
)

// Test that the m.room.create and m.room.member events for a room we created comes down /sync
func TestBackfillingHistory(t *testing.T) {
	deployment := Deploy(t, "rooms_state", b.BlueprintAlice)
	defer deployment.Destroy(t)

	userID := "@alice:hs1"
	alice := deployment.Client(t, "hs1", userID)
	roomID := alice.CreateRoom(t, struct{}{})

	// eventA
	eventA := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message A",
		},
	})
	// eventB
	alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message B",
		},
	})
	// eventC
	alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message C",
		},
	})

	// event1
	alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		PrevEvents: []string{
			eventA,
		},
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message 1",
		},
	})

	t.Run("parallel", func(t *testing.T) {
		// sytest: Room creation reports m.room.create to myself
		t.Run("Room creation reports m.room.create to myself", func(t *testing.T) {
			t.Parallel()
			alice := deployment.Client(t, "hs1", userID)
			alice.SyncUntilTimelineHas(t, roomID, func(ev gjson.Result) bool {
				if ev.Get("type").Str != "m.room.create" {
					return false
				}
				must.EqualStr(t, ev.Get("sender").Str, userID, "wrong sender")
				must.EqualStr(t, ev.Get("content").Get("creator").Str, userID, "wrong content.creator")
				return true
			})
		})
	})
}
