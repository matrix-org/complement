// +build msc2716

// This file contains tests for incrementally importing history to an existing room,
// a currently experimental feature defined by MSC2716, which you can read here:
// https://github.com/matrix-org/matrix-doc/pull/2716

package tests

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
)

// Test that the message events we insert between A and B come back in the correct order from /messages
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

	// wait 3ms to ensure that the timestamp changes enough for each of the 3 message we try to insert later
	time.Sleep(3 * time.Millisecond)

	// eventB
	eventB := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message B",
		},
	})
	// eventC
	eventC := alice.SendEventSynced(t, roomID, b.Event{
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
			"msgtype":      "m.text",
			"body":         "Message 1",
			"m.historical": true,
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
			"msgtype":      "m.text",
			"body":         "Message 2",
			"m.historical": true,
		},
	})

	// event3
	event3 := alice.SendEvent(t, roomID, b.Event{
		Type: "m.room.message",
		PrevEvents: []string{
			event2,
		},
		OriginServerTS: insertOriginServerTs + 2,
		Content: map[string]interface{}{
			"msgtype":      "m.text",
			"body":         "Message 3",
			"m.historical": true,
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

	t.Run("parallel", func(t *testing.T) {
		t.Run("Backfilled messages come back in correct order", func(t *testing.T) {
			t.Parallel()

			messagesRes := alice.MustDoRaw(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, nil, "application/json", url.Values{
				"dir":   []string{"b"},
				"limit": []string{"100"},
			})

			expectedMessageOrder := []string{
				eventStar, eventC, eventB, event3, event2, event1, eventA,
			}

			must.MatchResponse(t, messagesRes, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONArrayEach("chunk", func(r gjson.Result) error {
						// Find all events in order
						if len(r.Get("content").Get("body").Str) > 0 {
							// Pop the next message off the expected list
							nextEventInOrder := expectedMessageOrder[0]
							expectedMessageOrder = expectedMessageOrder[1:]

							if r.Get("event_id").Str != nextEventInOrder {
								return fmt.Errorf("Next event found was %s but expected %s", r.Get("event_id").Str, nextEventInOrder)
							}
						}

						return nil
					}),
				},
			})
		})
	})
}
