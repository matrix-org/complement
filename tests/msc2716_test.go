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

type event struct {
	Type           string
	Sender         string
	OriginServerTS uint64
	StateKey       *string
	PrevEvents     []string
	Content        map[string]interface{}
}

// Test that the message events we insert between A and B come back in the correct order from /messages
func TestBackfillingHistory(t *testing.T) {
	deployment := Deploy(t, "rooms_state", b.BlueprintHSWithApplicationService)
	defer deployment.Destroy(t)

	asUserID := "@the-bridge-user:hs1"
	as := deployment.Client(t, "hs1", asUserID)
	roomID := as.CreateRoom(t, struct{}{})

	userID := "@alice:hs1"
	alice := deployment.Client(t, "hs1", userID)
	alice.JoinRoom(t, roomID, nil)

	eventsBefore := createMessagesInRoom(t, alice, roomID, 1)
	eventBefore := eventsBefore[0]
	timeAfterEventBefore := time.Now()

	numBackfilledMessages := 3
	// wait X number of ms to ensure that the timestamp changes enough for each of the messages we try to backfill later
	time.Sleep(time.Duration(numBackfilledMessages) * time.Millisecond)

	eventsAfter := createMessagesInRoom(t, alice, roomID, 2)

	// We backfill a bunch of events after eventBefore
	backfilledEvents := backfillMessagesAtTime(t, as, roomID, eventBefore, timeAfterEventBefore, numBackfilledMessages)

	t.Run("parallel", func(t *testing.T) {
		t.Run("Backfilled messages come back in correct order", func(t *testing.T) {
			t.Parallel()

			messagesRes := alice.MustDoRaw(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, nil, "application/json", url.Values{
				"dir":   []string{"b"},
				"limit": []string{"100"},
			})

			// Order events from newest to oldest
			var expectedMessageOrder []string
			expectedMessageOrder = append(reversed(eventsAfter), reversed(backfilledEvents)...)
			expectedMessageOrder = append(expectedMessageOrder, reversed(eventsBefore)...)

			must.MatchResponse(t, messagesRes, match.HTTPResponse{
				JSON: []match.JSON{
					func(body []byte) error {
						eventIDsFromResponse, err := getEventIDsFromResponseBody(body)
						if err != nil {
							return err
						}

						// Copy the array by value so we can modify it as we iterate in the foreach loop
						workingExpectedMessageOrder := expectedMessageOrder

						// Match each event from the response in order to the list of expected events
						matcher := match.JSONArrayEach("chunk", func(r gjson.Result) error {
							// Find all events in order
							if len(r.Get("content").Get("body").Str) > 0 {
								// Pop the next message off the expected list
								nextEventInOrder := workingExpectedMessageOrder[0]
								// Update the list as we go for the next loop
								workingExpectedMessageOrder = workingExpectedMessageOrder[1:]

								if r.Get("event_id").Str != nextEventInOrder {
									return fmt.Errorf("Next event found was %s but expected %s\nActualEvents: %v\nExpectedEvents: %v", r.Get("event_id").Str, nextEventInOrder, eventIDsFromResponse, expectedMessageOrder)
								}
							}

							return nil
						})

						err = matcher(body)

						return err
					},
				},
			})
		})

		t.Run("Backfilled events with m.historical do not come down /sync", func(t *testing.T) {
			t.Parallel()

			roomID := alice.CreateRoom(t, struct{}{})
			eventsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventBefore := eventsBefore[0]
			timeAfterEventBefore := time.Now()
			insertOriginServerTs := uint64(timeAfterEventBefore.UnixNano() / 1000000)

			// If we see this message in the /sync response, then something went wrong
			event1 := sendEvent(t, alice, roomID, event{
				Type: "m.room.message",
				PrevEvents: []string{
					eventBefore,
				},
				OriginServerTS: insertOriginServerTs,
				Content: map[string]interface{}{
					"msgtype":      "m.text",
					"body":         "Message 1",
					"m.historical": true,
				},
			})

			// This is just a dummy event we search for after event1
			eventStar := sendEvent(t, alice, roomID, event{
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
			eventsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventBefore := eventsBefore[0]
			timeAfterEventBefore := time.Now()
			insertOriginServerTs := uint64(timeAfterEventBefore.UnixNano() / 1000000)

			event1 := sendEvent(t, alice, roomID, event{
				Type: "m.room.message",
				PrevEvents: []string{
					eventBefore,
				},
				OriginServerTS: insertOriginServerTs,
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    "Message 1",
					// This is commented out on purpse.
					// We are explicitely testing when m.historical isn't present
					//"m.historical": true,
				},
			})

			alice.SyncUntilTimelineHas(t, roomID, func(r gjson.Result) bool {
				return r.Get("event_id").Str == event1
			})
		})
	})
}

func reversed(in []string) []string {
	out := make([]string, len(in))
	for i := 0; i < len(in); i++ {
		out[i] = in[len(in)-i-1]
	}
	return out
}

func getEventIDsFromResponseBody(body []byte) (eventIDsFromResponse []string, err error) {
	wantKey := "chunk"
	res := gjson.GetBytes(body, wantKey)
	if !res.Exists() {
		return eventIDsFromResponse, fmt.Errorf("missing key '%s'", wantKey)
	}
	if !res.IsArray() {
		return eventIDsFromResponse, fmt.Errorf("key '%s' is not an array (was %s)", wantKey, res.Type)
	}

	res.ForEach(func(key, r gjson.Result) bool {
		if len(r.Get("content").Get("body").Str) > 0 {
			eventIDsFromResponse = append(eventIDsFromResponse, r.Get("event_id").Str)
		}
		return true
	})

	return eventIDsFromResponse, nil
}

var txnID int = 0
var txnPrefix string = "msc2716-txn"

func sendEvent(t *testing.T, c *client.CSAPI, roomID string, e event) string {
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

func createMessagesInRoom(t *testing.T, c *client.CSAPI, roomID string, count int) []string {
	evs := make([]string, count)
	for i := 0; i < len(evs); i++ {
		newEvent := b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    fmt.Sprintf("Message %d", i),
			},
		}
		newEventId := c.SendEventSynced(t, roomID, newEvent)
		evs[i] = newEventId
	}

	return evs
}

func backfillMessagesAtTime(t *testing.T, c *client.CSAPI, roomID string, insertAfterEventId string, insertTime time.Time, count int) []string {
	insertOriginServerTs := uint64(insertTime.UnixNano() / 1000000)

	evs := make([]string, count)

	prevEventId := insertAfterEventId
	for i := 0; i < len(evs); i++ {
		newEvent := event{
			Type: "m.room.message",
			PrevEvents: []string{
				prevEventId,
			},
			OriginServerTS: insertOriginServerTs + uint64(i),
			Content: map[string]interface{}{
				"msgtype":      "m.text",
				"body":         fmt.Sprintf("Backfilled %d", i),
				"m.historical": true,
			},
		}
		newEventId := sendEvent(t, c, roomID, newEvent)
		evs[i] = newEventId

		prevEventId = newEventId
	}

	return evs
}
