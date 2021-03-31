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
	"github.com/sirupsen/logrus"
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
	//defer time.Sleep(2 * time.Hour)

	// Create the application service bridge user that is able to backfill messages
	asUserID := "@the-bridge-user:hs1"
	as := deployment.Client(t, "hs1", asUserID)

	// Create the normal user which will send messages in the room
	userID := "@alice:hs1"
	alice := deployment.Client(t, "hs1", userID)

	t.Run("parallel", func(t *testing.T) {
		t.Run("Backfilled historical messages come back in correct order", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, struct{}{})
			alice.JoinRoom(t, roomID, nil)

			// Create the "live" event we are going to insert our backfilled events next to
			eventsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventBefore := eventsBefore[0]
			timeAfterEventBefore := time.Now()

			numHistoricalMessages := 3
			// wait X number of ms to ensure that the timestamp changes enough for each of the messages we try to backfill later
			time.Sleep(time.Duration(numHistoricalMessages) * time.Millisecond)

			// Create some more "live" events after our insertion point
			eventsAfter := createMessagesInRoom(t, alice, roomID, 2)

			// Then backfill a bunch of events between eventBefore and eventsAfter
			historicalEvents := reversed(backfillHistoricalMessagesInReverseChronologicalAtTime(t, as, "", roomID, eventBefore, timeAfterEventBefore, numHistoricalMessages))

			messagesRes := alice.MustDoRaw(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, nil, "application/json", url.Values{
				"dir":   []string{"b"},
				"limit": []string{"100"},
			})

			var expectedMessageOrder []string
			expectedMessageOrder = append(expectedMessageOrder, eventsBefore...)
			expectedMessageOrder = append(expectedMessageOrder, historicalEvents...)
			expectedMessageOrder = append(expectedMessageOrder, eventsAfter...)
			// Order events from newest to oldest
			expectedMessageOrder = reversed(expectedMessageOrder)

			contextRes := alice.MustDoRaw(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "context", eventsAfter[1]}, nil, "application/json", url.Values{
				"limit": []string{"100"},
			})
			contextResBody := client.ParseJSON(t, contextRes)
			logrus.WithFields(logrus.Fields{
				"contextResBody": string(contextResBody),
			}).Error("context res")

			must.MatchResponse(t, messagesRes, match.HTTPResponse{
				JSON: []match.JSON{
					// We're using this weird custom matcher function because we want to iterate the full response
					// to see all of the events from the response. This way we can use it to easily compare in
					// the fail error message when the test fails and compare the actual order to the expected order.
					func(body []byte) error {
						eventIDsFromResponse, err := getEventIDsFromResponseBody(body)
						if err != nil {
							return err
						}

						// Copy the array by value so we can modify it as we iterate in the foreach loop
						// We save the full untouched `expectedMessageOrder` for use in the log messages
						workingExpectedMessageOrder := expectedMessageOrder

						// Match each event from the response in order to the list of expected events
						matcher := match.JSONArrayEach("chunk", func(r gjson.Result) error {
							// Find all events in order
							if len(r.Get("content").Get("body").Str) > 0 {
								// Pop the next message off the expected list
								nextEventInOrder := workingExpectedMessageOrder[0]
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

		t.Run("Backfilled historical events resolve with proper state", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, map[string]interface{}{
				"preset": "public_chat",
				"name":   "the hangout spot",
			})
			alice.JoinRoom(t, roomID, nil)

			// Create the "live" event we are going to insert our backfilled events next to
			eventsBefore := createMessagesInRoom(t, alice, roomID, 2)
			eventBefore := eventsBefore[len(eventsBefore)-1]
			timeAfterEventBefore := time.Now()

			numHistoricalMessages := 6
			// wait X number of ms to ensure that the timestamp changes enough for each of the messages we try to backfill later
			time.Sleep(time.Duration(numHistoricalMessages) * time.Millisecond)

			// Create some events after.
			// Fill up the buffer so we have to scrollback to the inserted history later
			eventsAfter := createMessagesInRoom(t, alice, roomID, 2)

			virtualUserLocalpart := "maria"
			virtualUserID := fmt.Sprintf("@%s:hs1", virtualUserLocalpart)
			// Register and join the virtual user
			ensureRegistered(t, as, virtualUserLocalpart)

			// TODO: Try adding avatar and displayName and see if historical messages get this info

			// Insert the most recent chunk of backfilled history
			//backfillHistoricalMessagesInReverseChronologicalAtTime(t, as, virtualUserID, roomID, eventBefore, timeAfterEventBefore.Add(time.Millisecond*time.Duration(numHistoricalMessages)), 3)
			_, historicalEvents := backfillBulkHistoricalMessagesAtTime(
				t,
				as,
				virtualUserID,
				roomID,
				eventBefore,
				timeAfterEventBefore,
				3,
			)

			// Insert another older chunk of backfilled history from the same user.
			// See if the joins and meta data still are visible on the subsequent chunk
			//backfillHistoricalMessagesInReverseChronologicalAtTime(t, as, virtualUserID, roomID, eventBefore, timeAfterEventBefore, 3)

			var expectedMessageOrder []string
			expectedMessageOrder = append(expectedMessageOrder, eventsBefore...)
			expectedMessageOrder = append(expectedMessageOrder, historicalEvents...)
			expectedMessageOrder = append(expectedMessageOrder, eventsAfter...)
			// Order events from newest to oldest
			expectedMessageOrder = reversed(expectedMessageOrder)

			messagesRes := alice.MustDoRaw(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, nil, "application/json", url.Values{
				"dir":   []string{"b"},
				"limit": []string{"100"},
			})

			contextRes := alice.MustDoRaw(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "context", eventsAfter[len(eventsAfter)-1]}, nil, "application/json", url.Values{
				"limit": []string{"100"},
			})
			contextResBody := client.ParseJSON(t, contextRes)
			logrus.WithFields(logrus.Fields{
				"contextResBody": string(contextResBody),
			}).Error("context res")

			must.MatchResponse(t, messagesRes, match.HTTPResponse{
				JSON: []match.JSON{
					// We're using this weird custom matcher function because we want to iterate the full response
					// to see all of the events from the response. This way we can use it to easily compare in
					// the fail error message when the test fails and compare the actual order to the expected order.
					func(body []byte) error {
						eventIDsFromResponse, err := getEventIDsFromResponseBody(body)
						if err != nil {
							return err
						}

						// Copy the array by value so we can modify it as we iterate in the foreach loop
						// We save the full untouched `expectedMessageOrder` for use in the log messages
						workingExpectedMessageOrder := expectedMessageOrder

						// Match each event from the response in order to the list of expected events
						matcher := match.JSONArrayEach("chunk", func(r gjson.Result) error {
							// Find all events in order
							if len(r.Get("content").Get("body").Str) > 0 {
								// Pop the next message off the expected list
								nextEventInOrder := workingExpectedMessageOrder[0]
								workingExpectedMessageOrder = workingExpectedMessageOrder[1:]

								if r.Get("event_id").Str != nextEventInOrder {
									return fmt.Errorf("Next event found was %s but expected %s\nActualEvents (%d): %v\nExpectedEvents (%d): %v", r.Get("event_id").Str, nextEventInOrder, len(eventIDsFromResponse), eventIDsFromResponse, len(expectedMessageOrder), expectedMessageOrder)
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

		t.Run("Backfilled historical events with m.historical do not come down /sync", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, struct{}{})
			alice.JoinRoom(t, roomID, nil)

			// Create the "live" event we are going to insert our backfilled events next to
			eventsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventBefore := eventsBefore[0]
			timeAfterEventBefore := time.Now()

			// Create some "live" events to saturate and fill up the /sync response
			createMessagesInRoom(t, alice, roomID, 5)

			// Insert a backfilled event
			historicalEvents := backfillHistoricalMessagesInReverseChronologicalAtTime(t, as, "", roomID, eventBefore, timeAfterEventBefore, 1)
			backfilledEvent := historicalEvents[0]

			// This is just a dummy event we search for after the backfilledEvent
			eventsAfterBackfill := createMessagesInRoom(t, alice, roomID, 1)
			eventAfterBackfill := eventsAfterBackfill[0]

			// Sync until we find the eventAfterBackfill. If we're able to see the eventAfterBackfill
			// that occurs after the backfilledEvent without seeing eventAfterBackfill in between,
			// we're probably safe to assume it won't sync
			alice.SyncUntil(t, "", `{ "room": { "timeline": { "limit": 3 } } }`, "rooms.join."+client.GjsonEscape(roomID)+".timeline.events", func(r gjson.Result) bool {
				if r.Get("event_id").Str == backfilledEvent {
					t.Fatalf("We should not see the %s backfilled event in /sync response but it was present", backfilledEvent)
				}

				return r.Get("event_id").Str == eventAfterBackfill
			})
		})

		t.Run("Backfilled historical events without m.historical come down /sync", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, struct{}{})
			alice.JoinRoom(t, roomID, nil)

			eventsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventBefore := eventsBefore[0]
			timeAfterEventBefore := time.Now()
			insertOriginServerTs := uint64(timeAfterEventBefore.UnixNano() / 1000000)

			// Send an event that has `prev_event` and `ts` set but not `m.historical`.
			// We should see these type of events in the `/sync` response
			eventWeShouldSee := sendEvent(t, as, "", roomID, event{
				Type: "m.room.message",
				PrevEvents: []string{
					eventBefore,
				},
				OriginServerTS: insertOriginServerTs,
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    "Message with prev_event and ts but no m.historical",
					// This is commented out on purpse.
					// We are explicitely testing when m.historical isn't present
					//"m.historical": true,
				},
			})

			alice.SyncUntilTimelineHas(t, roomID, func(r gjson.Result) bool {
				return r.Get("event_id").Str == eventWeShouldSee
			})
		})

		t.Run("Unrecognised prev_event ID will throw an error", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, struct{}{})

			e := event{
				Type: "m.room.message",
				PrevEvents: []string{
					// Here is the area of interest in the event
					"$some-non-existant-event-id",
				},
				OriginServerTS: uint64(time.Now().UnixNano() / 1000000),
				Content: map[string]interface{}{
					"msgtype":      "m.text",
					"body":         "Historical message",
					"m.historical": true,
				},
			}

			query := make(url.Values, len(e.PrevEvents))
			query.Add("prev_event", e.PrevEvents[0])
			query.Add("ts", strconv.FormatUint(e.OriginServerTS, 10))

			b, err := json.Marshal(e.Content)
			if err != nil {
				t.Fatalf("msc2716.sendEvent failed to marshal JSON body: %s", err)
			}

			as.MustDoWithStatusRaw(
				t,
				"PUT",
				[]string{"_matrix", "client", "r0", "rooms", roomID, "send", e.Type, txnPrefix + "404-unrecognized-prev-event-id"},
				b,
				"application/json",
				query,
				// TODO: Seems like this makes more sense as a 404
				// But the current Synapse code around unknown prev events will throw ->
				// `403: No create event in auth events`
				403,
			)
		})

		t.Run("Normal users aren't allowed to backfill messages", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, struct{}{})
			alice.JoinRoom(t, roomID, nil)

			eventsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventBefore := eventsBefore[0]
			timeAfterEventBefore := time.Now()
			insertOriginServerTs := uint64(timeAfterEventBefore.UnixNano() / 1000000)

			e := event{
				Type: "m.room.message",
				PrevEvents: []string{
					eventBefore,
				},
				OriginServerTS: insertOriginServerTs,
				Content: map[string]interface{}{
					"msgtype":      "m.text",
					"body":         "Historical message",
					"m.historical": true,
				},
			}

			query := make(url.Values, len(e.PrevEvents))
			query.Add("prev_event", e.PrevEvents[0])
			query.Add("ts", strconv.FormatUint(e.OriginServerTS, 10))

			b, err := json.Marshal(e.Content)
			if err != nil {
				t.Fatalf("msc2716.sendEvent failed to marshal JSON body: %s", err)
			}

			// Normal user alice should not be able to backfill messages
			alice.MustDoWithStatusRaw(t, "PUT", []string{"_matrix", "client", "r0", "rooms", roomID, "send", e.Type, txnPrefix + "403-no-normal-user-test"}, b, "application/json", query, 403)
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
			eventIDsFromResponse = append(eventIDsFromResponse, r.Get("event_id").Str+" ("+r.Get("content").Get("body").Str+")")
		}
		return true
	})

	return eventIDsFromResponse, nil
}

var txnID int = 0

// The transactions need to be prefixed so they don't collide with the txnID in client.go
var txnPrefix string = "msc2716-txn"

func sendEvent(t *testing.T, c *client.CSAPI, virtualUserID string, roomID string, e event) string {
	txnID++

	query := make(url.Values, len(e.PrevEvents))
	for _, prevEvent := range e.PrevEvents {
		query.Add("prev_event", prevEvent)
	}

	if e.OriginServerTS != 0 {
		query.Add("ts", strconv.FormatUint(e.OriginServerTS, 10))
	}

	if virtualUserID != "" {
		query.Add("user_id", virtualUserID)
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

// ensureRegistered makes sure the user is registered for the homeserver regardless
// if they are already registered or not. If unable to register, fails the test
func ensureRegistered(t *testing.T, c *client.CSAPI, virtualUserLocalpart string) {
	// b, err := json.Marshal(map[string]interface{}{
	// 	"username": virtualUserLocalpart,
	// })
	// if err != nil {
	// 	t.Fatalf("msc2716.ensureRegistered failed to marshal JSON body: %s", err)
	// }

	res, err := c.DoWithAuthRaw(t, "POST", []string{"_matrix", "client", "r0", "register"}, json.RawMessage(fmt.Sprintf(`{ "username": "%s" }`, virtualUserLocalpart)), "application/json", url.Values{})

	if err != nil {
		t.Error(err)
	}

	if res.StatusCode == 200 {
		return
	}

	body := client.ParseJSON(t, res)
	errcode := client.GetJSONFieldStr(t, body, "errcode")

	if res.StatusCode == 400 && errcode == "M_USER_IN_USE" {
		return
	} else {
		errorMessage := client.GetJSONFieldStr(t, body, "error")
		t.Fatalf("msc2716.ensureRegistered failed to register: (%s) %s", errcode, errorMessage)
	}
}

// joinRoom joins the room ID or alias given, else fails the test. Returns the room ID.
func joinRoom(t *testing.T, c *client.CSAPI, virtualUserID string, roomIDOrAlias string) string {
	query := url.Values{}
	if virtualUserID != "" {
		query.Add("user_id", virtualUserID)
	}

	// join the room
	res := c.MustDoRaw(t, "POST", []string{"_matrix", "client", "r0", "join", roomIDOrAlias}, nil, "application/json", query)
	// return the room ID if we joined with it
	if roomIDOrAlias[0] == '!' {
		return roomIDOrAlias
	}
	// otherwise we should be told the room ID if we joined via an alias
	body := client.ParseJSON(t, res)
	return client.GetJSONFieldStr(t, body, "room_id")
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

// Backfill in a reverse-chronogical order (most recent history to oldest history)
// Reverse-chronogical is a constraint of the Synapse implementation.
func backfillHistoricalMessagesInReverseChronologicalAtTime(t *testing.T, c *client.CSAPI, virtualUserID string, roomID string, insertAfterEventId string, insertTime time.Time, count int) []string {
	insertOriginServerTs := uint64(insertTime.UnixNano() / 1000000)

	evs := make([]string, count)

	for i := 0; i < len(evs); i++ {
		// We have to backfill historical messages from most recent to oldest
		// since backfilled messages decrement their `stream_order` and we want messages
		// to appear in order from the `/messages` endpoint
		messageIndex := (count - 1) - i

		newEvent := event{
			Type: "m.room.message",
			PrevEvents: []string{
				// Hang all historical messages off of the insert point
				insertAfterEventId,
			},
			OriginServerTS: insertOriginServerTs + uint64(messageIndex),
			Content: map[string]interface{}{
				"msgtype":      "m.text",
				"body":         fmt.Sprintf("Historical %d", messageIndex),
				"m.historical": true,
			},
		}
		newEventId := sendEvent(t, c, virtualUserID, roomID, newEvent)
		evs[i] = newEventId
	}

	return evs
}

func backfillBulkHistoricalMessagesAtTime(
	t *testing.T,
	c *client.CSAPI,
	virtualUserID string,
	roomID string,
	insertAfterEventId string,
	insertTime time.Time,
	count int,
) (state_event_ids []string, event_ids []string) {
	insertOriginServerTs := uint64(insertTime.UnixNano() / 1000000)

	evs := make([]map[string]interface{}, count)
	for i := 0; i < len(evs); i++ {
		newEvent := map[string]interface{}{
			"type":             "m.room.message",
			"sender":           virtualUserID,
			"origin_server_ts": insertOriginServerTs + uint64(i),
			"content": map[string]interface{}{
				"msgtype":      "m.text",
				"body":         fmt.Sprintf("Historical %d", i),
				"m.historical": true,
			},
		}
		evs[i] = newEvent
	}

	joinEvent := map[string]interface{}{
		"type":             "m.room.member",
		"sender":           virtualUserID,
		"origin_server_ts": insertOriginServerTs,
		"content": map[string]interface{}{
			"membership": "join",
		},
		"state_key": virtualUserID,
	}

	query := make(url.Values, 2)
	query.Add("prev_event", insertAfterEventId)
	query.Add("user_id", virtualUserID)

	b, err := json.Marshal(map[string]interface{}{
		"events":                evs,
		"state_events_at_start": []map[string]interface{}{joinEvent},
	})
	if err != nil {
		t.Fatalf("msc2716.backfillBulkHistoricalMessagesAtTime failed to marshal JSON body: %s", err)
	}

	res := c.MustDoRaw(t, "POST", []string{"_matrix", "client", "r0", "rooms", roomID, "bulksend"}, b, "application/json", query)
	body := client.ParseJSON(t, res)

	stateEvents := client.GetJSONFieldArray(t, body, "state_events")
	events := client.GetJSONFieldArray(t, body, "events")

	return stateEvents, events
}
