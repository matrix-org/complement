// +build msc2716

// This file contains tests for incrementally importing history to an existing room,
// a currently experimental feature defined by MSC2716, which you can read here:
// https://github.com/matrix-org/matrix-doc/pull/2716

package tests

import (
	"net/url"
	"testing"
	"time"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/must"
	"github.com/sirupsen/logrus"
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

	insertTime := time.Now()
	insertOriginServerTs := uint64(insertTime.UnixNano() / 1000000)

	// wait 3ms to ensure that the timestamp changes enough intervals for each message we try to insert later
	time.Sleep(3 * time.Millisecond)

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
	event1 := alice.SendEvent(t, roomID, b.Event{
		Type: "m.room.message",
		PrevEvents: []string{
			eventA,
		},
		OriginServerTS: insertOriginServerTs,
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message 1",
		},
	})

	// event2
	event2 := alice.SendEvent(t, roomID, b.Event{
		Type: "m.room.message",
		PrevEvents: []string{
			event1,
		},
		OriginServerTS: insertOriginServerTs + 1,
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message 2",
		},
	})

	// event3
	alice.SendEvent(t, roomID, b.Event{
		Type: "m.room.message",
		PrevEvents: []string{
			event2,
		},
		OriginServerTS: insertOriginServerTs + 2,
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message 3",
		},
	})

	// eventStar
	eventStar := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message *",
		},
	})

	res := alice.MustDoRaw(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, nil, "application/json", url.Values{
		"dir":   []string{"b"},
		"limit": []string{"100"},
	})

	t.Logf("aweawfeefwaweafeafw")
	body := client.ParseJSON(t, res)
	logrus.WithFields(logrus.Fields{
		"insertOriginServerTs": insertOriginServerTs,
		"res":                  res,
		"body":                 string(body),
	}).Error("messages res")

	contextRes := alice.MustDoRaw(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "context", eventStar}, nil, "application/json", url.Values{
		"limit": []string{"100"},
	})
	contextResBody := client.ParseJSON(t, contextRes)
	logrus.WithFields(logrus.Fields{
		"contextResBody": string(contextResBody),
	}).Error("context res")

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
