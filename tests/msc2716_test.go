// +build msc2716

// This file contains tests for incrementally importing history to an existing room,
// a currently experimental feature defined by MSC2716, which you can read here:
// https://github.com/matrix-org/matrix-doc/pull/2716

package tests

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
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

	eventA, eventB, eventC, timeAfterEventA := createMessagesInRoom(t, alice, roomID)

	event1, event2, event3 := backfillMessagesAtTime(t, alice, roomID, eventA, timeAfterEventA)

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

		t.Run("Backfilled events with m.historical do not come down /sync", func(t *testing.T) {
			t.Parallel()

			roomID := alice.CreateRoom(t, struct{}{})
			eventA, _, _, timeAfterEventA := createMessagesInRoom(t, alice, roomID)
			insertOriginServerTs := uint64(timeAfterEventA.UnixNano() / 1000000)

			// If we see this message in the /sync, then something went wrong
			event1 := sendEvent(t, alice, roomID, b.Event{
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

			// This is just a dummy event we search for after event1
			eventStar := sendEvent(t, alice, roomID, b.Event{
				Type: "m.room.message",
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    "Message *",
				},
			})

			// Sync until we find the star message. If we're able to see the star message
			// that occurs after event1 without seeing event1 in the mean-time, I think we're safe to
			// assume it won't sync
			alice.SyncUntil(t, "", "rooms.join."+client.GjsonEscape(roomID)+".timeline.events", func(r gjson.Result) bool {
				if r.Get("event_id").Str == event1 {
					t.Fatalf("We should not see the %s event in /sync response but it was present", event1)
				}

				return r.Get("event_id").Str == eventStar
			})
		})

		t.Run("Backfilled events without m.historical come down /sync", func(t *testing.T) {
			t.Parallel()

			roomID := alice.CreateRoom(t, struct{}{})
			eventA, _, _, timeAfterEventA := createMessagesInRoom(t, alice, roomID)
			insertOriginServerTs := uint64(timeAfterEventA.UnixNano() / 1000000)

			alice.SendEventSynced(t, roomID, b.Event{
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
		})
	})
}

var txnID int = 0
var txnPrefix string = "msc2716-txn"

func sendEvent(t *testing.T, c *client.CSAPI, roomID string, e b.Event) string {
	txnID++

	query := make(url.Values, len(e.PrevEvents))
	for _, prevEvent := range e.PrevEvents {
		query.Add("prev_event", prevEvent)
	}

	if e.OriginServerTS != 0 {
		query.Add("ts", strconv.FormatUint(e.OriginServerTS, 10))
	}

	b, err := json.Marshal(e.Content)
	if err != nil {
		t.Fatalf("msc2716.sendEvent failed to marshal JSON body: %s", err)
	}

	res := c.MustDoRaw(t, "PUT", []string{"_matrix", "client", "r0", "rooms", roomID, "send", e.Type, txnPrefix + strconv.Itoa(txnID)}, b, "application/json", query)
	body := client.ParseJSON(t, res)
	eventID := client.GetJSONFieldStr(t, body, "event_id")

	return eventID
}

func createMessagesInRoom(t *testing.T, c *client.CSAPI, roomID string) (string, string, string, time.Time) {
	// eventA
	eventA := c.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message A",
		},
	})

	timeAfterEventA := time.Now()

	// wait 3ms to ensure that the timestamp changes enough for each of the 3 message we try to insert later
	time.Sleep(3 * time.Millisecond)

	// eventB
	eventB := c.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message B",
		},
	})
	// eventC
	eventC := c.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message C",
		},
	})

	return eventA, eventB, eventC, timeAfterEventA
}

func backfillMessagesAtTime(t *testing.T, c *client.CSAPI, roomID string, insertAfterEvent string, insertTime time.Time) (string, string, string) {
	insertOriginServerTs := uint64(insertTime.UnixNano() / 1000000)

	// event1
	event1 := sendEvent(t, c, roomID, b.Event{
		Type: "m.room.message",
		PrevEvents: []string{
			insertAfterEvent,
		},
		OriginServerTS: insertOriginServerTs,
		Content: map[string]interface{}{
			"msgtype":      "m.text",
			"body":         "Message 1",
			"m.historical": true,
		},
	})

	// event2
	event2 := sendEvent(t, c, roomID, b.Event{
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
	event3 := sendEvent(t, c, roomID, b.Event{
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

	return event1, event2, event3
}
