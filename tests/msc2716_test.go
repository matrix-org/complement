// +build msc2716

// This file contains tests for incrementally importing history to an existing room,
// a currently experimental feature defined by MSC2716, which you can read here:
// https://github.com/matrix-org/matrix-doc/pull/2716

package tests

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
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

// This is configurable because it can be nice to change it to `time.Second` while
// checking out the test result in a Synapse instance
const timeBetweenMessages = time.Millisecond

var (
	insertionEventType = "org.matrix.msc2716.insertion"
	markerEventType    = "org.matrix.msc2716.marker"

	historicalContentField                = "org.matrix.msc2716.historical"
	nextChunkIdContentField               = "org.matrix.msc2716.next_chunk_id"
	chunkIdContentField                   = "org.matrix.msc2716.chunk_id"
	markerInsertionContentField           = "org.matrix.msc2716.marker.insertion"
	markerInsertionPrevEventsContentField = "org.matrix.msc2716.marker.insertion_prev_events"
)

func TestBackfillingHistory(t *testing.T) {
	deployment := Deploy(t, b.BlueprintHSWithApplicationService)
	defer deployment.Destroy(t)

	// Create the application service bridge user that is able to backfill messages
	asUserID := "@the-bridge-user:hs1"
	as := deployment.Client(t, "hs1", asUserID)

	// Create the normal user which will send messages in the room
	userID := "@alice:hs1"
	alice := deployment.Client(t, "hs1", userID)

	// Create the federated user which will fetch the messages from a remote homeserver
	remoteUserID := "@charlie:hs2"
	remoteCharlie := deployment.Client(t, "hs2", remoteUserID)

	virtualUserLocalpart := "maria"
	virtualUserID := fmt.Sprintf("@%s:hs1", virtualUserLocalpart)

	t.Run("parallel", func(t *testing.T) {
		// Test that the message events we insert between A and B come back in the correct order from /messages
		//
		// Final timeline output: ( [n] = historical chunk )
		// (oldest) A, B, [insertion, c, d, e] [insertion, f, g, h, insertion], I, J (newest)
		//                chunk 1              chunk 0
		t.Run("Backfilled historical events resolve with proper state in correct order", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, map[string]interface{}{
				"preset": "public_chat",
				"name":   "the hangout spot",
			})
			alice.JoinRoom(t, roomID, nil)

			// Create some normal messages in the timeline. We're creating them in
			// two batches so we can create some time in between where we are going
			// to backfill.
			//
			// Create the first batch including the "live" event we are going to
			// insert our backfilled events next to.
			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 2)
			eventIdBefore := eventIDsBefore[len(eventIDsBefore)-1]
			timeAfterEventBefore := time.Now()

			// wait X number of ms to ensure that the timestamp changes enough for
			// each of the messages we try to backfill later
			numHistoricalMessages := 6
			time.Sleep(time.Duration(numHistoricalMessages) * timeBetweenMessages)

			// Create the second batch of events.
			// This will also fill up the buffer so we have to scrollback to the
			// inserted history later.
			eventIDsAfter := createMessagesInRoom(t, alice, roomID, 2)

			// Register and join the virtual user
			ensureVirtualUserRegistered(t, as, virtualUserLocalpart)

			// Insert the most recent chunk of backfilled history
			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				[]string{virtualUserID},
				roomID,
				eventIdBefore,
				timeAfterEventBefore.Add(timeBetweenMessages*3),
				"",
				3,
				// Status
				200,
			)
			batchSendResBody := client.ParseJSON(t, batchSendRes)
			historicalEventIDs := getEventsFromBatchSendResponseBody(t, batchSendResBody)
			nextChunkID := getNextChunkIdFromBatchSendResponseBody(t, batchSendResBody)

			// Insert another older chunk of backfilled history from the same user.
			// Make sure the meta data and joins still work on the subsequent chunk
			batchSendRes2 := batchSendHistoricalMessages(
				t,
				as,
				[]string{virtualUserID},
				roomID,
				eventIdBefore,
				timeAfterEventBefore,
				nextChunkID,
				3,
				// Status
				200,
			)
			batchSendResBody2 := client.ParseJSON(t, batchSendRes2)
			historicalEventIDs2 := getEventsFromBatchSendResponseBody(t, batchSendResBody2)

			var expectedEventIDOrder []string
			expectedEventIDOrder = append(expectedEventIDOrder, eventIDsBefore...)
			expectedEventIDOrder = append(expectedEventIDOrder, historicalEventIDs2...)
			expectedEventIDOrder = append(expectedEventIDOrder, historicalEventIDs...)
			expectedEventIDOrder = append(expectedEventIDOrder, eventIDsAfter...)
			// Order events from newest to oldest
			expectedEventIDOrder = reversed(expectedEventIDOrder)

			// 2 eventIDsBefore + 6 historical events + 3 insertion events + 2 eventIDsAfter
			if len(expectedEventIDOrder) != 13 {
				t.Fatalf("Expected eventID list should be length 13 but saw %d: %s", len(expectedEventIDOrder), expectedEventIDOrder)
			}

			messagesRes := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
				"dir":   []string{"b"},
				"limit": []string{"100"},
			}))
			messsageResBody := client.ParseJSON(t, messagesRes)
			eventDebugStringsFromResponse := getRelevantEventDebugStringsFromMessagesResponse(t, messsageResBody)
			// Since the original body can only be read once, create a new one from the body bytes we just read
			messagesRes.Body = ioutil.NopCloser(bytes.NewBuffer(messsageResBody))

			// Copy the array by slice so we can modify it as we iterate in the foreach loop.
			// We save the full untouched `expectedEventIDOrder` for use in the log messages
			workingExpectedEventIDOrder := expectedEventIDOrder

			must.MatchResponse(t, messagesRes, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONArrayEach("chunk", func(r gjson.Result) error {
						// Find all events in order
						if len(r.Get("content").Get("body").Str) > 0 || r.Get("type").Str == insertionEventType || r.Get("type").Str == markerEventType {
							// Pop the next message off the expected list
							nextEventIdInOrder := workingExpectedEventIDOrder[0]
							workingExpectedEventIDOrder = workingExpectedEventIDOrder[1:]

							if r.Get("event_id").Str != nextEventIdInOrder {
								return fmt.Errorf("Next event found was %s but expected %s\nActualEvents (%d): %v\nExpectedEvents (%d): %v", r.Get("event_id").Str, nextEventIdInOrder, len(eventDebugStringsFromResponse), eventDebugStringsFromResponse, len(expectedEventIDOrder), expectedEventIDOrder)
							}
						}

						return nil
					}),
				},
			})

			if len(workingExpectedEventIDOrder) != 0 {
				t.Fatalf("Expected all events to be matched in message response but there were some left-over events: %s", workingExpectedEventIDOrder)
			}
		})

		t.Run("Backfilled historical events from multiple users in the same chunk", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, struct{}{})
			alice.JoinRoom(t, roomID, nil)

			// Create the "live" event we are going to insert our backfilled events next to
			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			// Insert a backfilled event
			virtualUserID2 := "@ricky:hs1"
			virtualUserID3 := "@carol:hs1"
			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				[]string{virtualUserID, virtualUserID2, virtualUserID3},
				roomID,
				eventIdBefore,
				timeAfterEventBefore,
				"",
				3,
				// Status
				200,
			)
			batchSendResBody := client.ParseJSON(t, batchSendRes)
			historicalEventIDs := getEventsFromBatchSendResponseBody(t, batchSendResBody)

			messagesRes := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
				"dir":   []string{"b"},
				"limit": []string{"100"},
			}))

			must.MatchResponse(t, messagesRes, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONCheckOffAllowUnwanted("chunk", []interface{}{historicalEventIDs[0], historicalEventIDs[1], historicalEventIDs[2]}, func(r gjson.Result) interface{} {
						return r.Get("event_id").Str
					}, nil),
				},
			})
		})

		t.Run("Backfilled historical events with m.historical do not come down in an incremental sync", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, struct{}{})
			alice.JoinRoom(t, roomID, nil)

			// Create the "live" event we are going to insert our backfilled events next to
			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			// Create some "live" events to saturate and fill up the /sync response
			createMessagesInRoom(t, alice, roomID, 5)

			// Insert a backfilled event
			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				[]string{virtualUserID},
				roomID,
				eventIdBefore,
				timeAfterEventBefore,
				"",
				1,
				// Status
				200,
			)
			batchSendResBody := client.ParseJSON(t, batchSendRes)
			historicalEventIDs := getEventsFromBatchSendResponseBody(t, batchSendResBody)
			backfilledEventId := historicalEventIDs[0]

			// This is just a dummy event we search for after the backfilledEventId
			eventIDsAfterBackfill := createMessagesInRoom(t, alice, roomID, 1)
			eventIdAfterBackfill := eventIDsAfterBackfill[0]

			// Sync until we find the eventIdAfterBackfill. If we're able to see the eventIdAfterBackfill
			// that occurs after the backfilledEventId without seeing eventIdAfterBackfill in between,
			// we're probably safe to assume it won't sync
			alice.SyncUntil(t, "", `{ "room": { "timeline": { "limit": 3 } } }`, "rooms.join."+client.GjsonEscape(roomID)+".timeline.events", func(r gjson.Result) bool {
				if r.Get("event_id").Str == backfilledEventId {
					t.Fatalf("We should not see the %s backfilled event in /sync response but it was present", backfilledEventId)
				}

				return r.Get("event_id").Str == eventIdAfterBackfill
			})
		})

		t.Run("Unrecognised prev_event ID will throw an error", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, struct{}{})

			batchSendHistoricalMessages(
				t,
				as,
				[]string{virtualUserID},
				roomID,
				"$some-non-existant-event-id",
				time.Now(),
				"",
				1,
				// Status
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

			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			batchSendHistoricalMessages(
				t,
				alice,
				[]string{virtualUserID},
				roomID,
				eventIdBefore,
				timeAfterEventBefore,
				"",
				1,
				// Status
				// Normal user alice should not be able to backfill messages
				403,
			)
		})

		t.Run("TODO: Test if historical avatar/display name set back in time are picked up on historical messages", func(t *testing.T) {
			t.Skip("Skipping until implemented")
			// TODO: Try adding avatar and displayName and see if historical messages get this info
		})

		t.Run("Historical messages are visible when joining on federated server", func(t *testing.T) {
			t.Skip("Skipping until federation is implemented")
			t.Parallel()

			roomID := as.CreateRoom(t, struct{}{})
			alice.JoinRoom(t, roomID, nil)

			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			// eventIDsAfter
			createMessagesInRoom(t, alice, roomID, 3)

			// Register and join the virtual user
			ensureVirtualUserRegistered(t, as, virtualUserLocalpart)

			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				[]string{virtualUserID},
				roomID,
				eventIdBefore,
				timeAfterEventBefore,
				"",
				2,
				// Status
				200,
			)
			batchSendResBody := client.ParseJSON(t, batchSendRes)
			historicalEventIDs := getEventsFromBatchSendResponseBody(t, batchSendResBody)

			// Join the room from a remote homeserver after the backfilled messages were sent
			remoteCharlie.JoinRoom(t, roomID, []string{"hs1"})

			messagesRes := remoteCharlie.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
				"dir":   []string{"b"},
				"limit": []string{"100"},
			}))

			must.MatchResponse(t, messagesRes, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONCheckOffAllowUnwanted("chunk", []interface{}{historicalEventIDs[0], historicalEventIDs[1]}, func(r gjson.Result) interface{} {
						return r.Get("event_id").Str
					}, nil),
				},
			})
		})

		t.Run("Historical messages are visible when already joined on federated server", func(t *testing.T) {
			t.Skip("Skipping until federation is implemented")
			t.Parallel()

			roomID := as.CreateRoom(t, struct{}{})
			alice.JoinRoom(t, roomID, nil)

			// Join the room from a remote homeserver before any backfilled messages are sent
			remoteCharlie.JoinRoom(t, roomID, []string{"hs1"})

			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			// eventIDsAfter
			createMessagesInRoom(t, alice, roomID, 10)

			// Mimic scrollback just through the latest messages
			remoteCharlie.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
				"dir": []string{"b"},
				// Limited so we can only see a few of the latest messages
				"limit": []string{"5"},
			}))

			// Register and join the virtual user
			ensureVirtualUserRegistered(t, as, virtualUserLocalpart)

			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				[]string{virtualUserID},
				roomID,
				eventIdBefore,
				timeAfterEventBefore,
				"",
				2,
				// Status
				200,
			)
			batchSendResBody := client.ParseJSON(t, batchSendRes)
			historicalEventIDs := getEventsFromBatchSendResponseBody(t, batchSendResBody)

			messagesRes := remoteCharlie.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
				"dir":   []string{"b"},
				"limit": []string{"100"},
			}))

			must.MatchResponse(t, messagesRes, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONCheckOffAllowUnwanted("chunk", []interface{}{historicalEventIDs[0], historicalEventIDs[1]}, func(r gjson.Result) interface{} {
						return r.Get("event_id").Str
					}, nil),
				},
			})
		})

		t.Run("When messages have already been scrolled back through, new historical messages are visible in next scroll back on federated server", func(t *testing.T) {
			t.Skip("Skipping until federation is implemented")
			t.Parallel()

			roomID := as.CreateRoom(t, struct{}{})
			alice.JoinRoom(t, roomID, nil)

			// Join the room from a remote homeserver before any backfilled messages are sent
			remoteCharlie.JoinRoom(t, roomID, []string{"hs1"})

			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			// eventIDsAfter
			createMessagesInRoom(t, alice, roomID, 3)

			// Register and join the virtual user
			ensureVirtualUserRegistered(t, as, virtualUserLocalpart)

			// Mimic scrollback to all of the messages
			// scrollbackMessagesRes
			remoteCharlie.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
				"dir":   []string{"b"},
				"limit": []string{"100"},
			}))

			// Historical messages are inserted where we have already scrolled back to
			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				[]string{virtualUserID},
				roomID,
				eventIdBefore,
				timeAfterEventBefore,
				"",
				2,
				// Status
				200,
			)
			batchSendResBody := client.ParseJSON(t, batchSendRes)
			historicalEventIDs := getEventsFromBatchSendResponseBody(t, batchSendResBody)

			messagesRes := remoteCharlie.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
				"dir":   []string{"b"},
				"limit": []string{"100"},
			}))

			must.MatchResponse(t, messagesRes, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONCheckOffAllowUnwanted("chunk", []interface{}{historicalEventIDs[0], historicalEventIDs[1]}, func(r gjson.Result) interface{} {
						return r.Get("event_id").Str
					}, nil),
				},
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

func getRelevantEventDebugStringsFromMessagesResponse(t *testing.T, body []byte) (eventIDsFromResponse []string) {
	t.Helper()

	wantKey := "chunk"
	res := gjson.GetBytes(body, wantKey)
	if !res.Exists() {
		t.Fatalf("missing key '%s'", wantKey)
	}
	if !res.IsArray() {
		t.Fatalf("key '%s' is not an array (was %s)", wantKey, res.Type)
	}

	res.ForEach(func(key, r gjson.Result) bool {
		if len(r.Get("content").Get("body").Str) > 0 || r.Get("type").Str == insertionEventType || r.Get("type").Str == markerEventType {
			eventIDsFromResponse = append(eventIDsFromResponse, r.Get("event_id").Str+" ("+r.Get("content").Get("body").Str+")")
		}
		return true
	})

	return eventIDsFromResponse
}

// ensureVirtualUserRegistered makes sure the user is registered for the homeserver regardless
// if they are already registered or not. If unable to register, fails the test
func ensureVirtualUserRegistered(t *testing.T, c *client.CSAPI, virtualUserLocalpart string) {
	res := c.DoFunc(
		t,
		"POST",
		[]string{"_matrix", "client", "r0", "register"},
		client.WithJSONBody(t, map[string]interface{}{"type": "m.login.application_service", "username": virtualUserLocalpart}),
		client.WithContentType("application/json"),
	)

	if res.StatusCode == 200 {
		return
	}

	body := client.ParseJSON(t, res)
	errcode := client.GetJSONFieldStr(t, body, "errcode")

	if res.StatusCode == 400 && errcode == "M_USER_IN_USE" {
		return
	} else {
		errorMessage := client.GetJSONFieldStr(t, body, "error")
		t.Fatalf("msc2716.ensureVirtualUserRegistered failed to register: (%s) %s", errcode, errorMessage)
	}
}

func createMessagesInRoom(t *testing.T, c *client.CSAPI, roomID string, count int) (eventIDs []string) {
	eventIDs = make([]string, count)
	for i := 0; i < len(eventIDs); i++ {
		newEvent := b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    fmt.Sprintf("Message %d", i),
			},
		}
		newEventId := c.SendEventSynced(t, roomID, newEvent)
		eventIDs[i] = newEventId
	}

	return eventIDs
}

var chunkCount int64 = 0

func batchSendHistoricalMessages(
	t *testing.T,
	c *client.CSAPI,
	virtualUserIDs []string,
	roomID string,
	insertAfterEventId string,
	insertTime time.Time,
	chunkID string,
	count int,
	expectedStatus int,
) (res *http.Response) {
	// Timestamp in milliseconds
	insertOriginServerTs := uint64(insertTime.UnixNano() / int64(time.Millisecond))

	timeBetweenMessagesMS := uint64(timeBetweenMessages / time.Millisecond)

	evs := make([]map[string]interface{}, count)
	for i := 0; i < len(evs); i++ {
		virtualUserID := virtualUserIDs[i%len(virtualUserIDs)]

		newEvent := map[string]interface{}{
			"type":             "m.room.message",
			"sender":           virtualUserID,
			"origin_server_ts": insertOriginServerTs + (timeBetweenMessagesMS * uint64(i)),
			"content": map[string]interface{}{
				"msgtype":              "m.text",
				"body":                 fmt.Sprintf("Historical %d (chunk=%d)", i, chunkCount),
				historicalContentField: true,
			},
		}

		evs[i] = newEvent
	}

	state_evs := make([]map[string]interface{}, len(virtualUserIDs))
	for i, virtualUserID := range virtualUserIDs {
		joinEvent := map[string]interface{}{
			"type":             "m.room.member",
			"sender":           virtualUserID,
			"origin_server_ts": insertOriginServerTs,
			"content": map[string]interface{}{
				"membership": "join",
			},
			"state_key": virtualUserID,
		}

		state_evs[i] = joinEvent
	}

	query := make(url.Values, 2)
	query.Add("prev_event", insertAfterEventId)
	// If provided, connect the chunk to the last insertion point
	if chunkID != "" {
		query.Add("chunk_id", chunkID)
	}

	res = c.DoFunc(
		t,
		"POST",
		[]string{"_matrix", "client", "unstable", "org.matrix.msc2716", "rooms", roomID, "batch_send"},
		client.WithJSONBody(t, map[string]interface{}{
			"events":                evs,
			"state_events_at_start": state_evs,
		}),
		client.WithContentType("application/json"),
		client.WithQueries(query),
	)

	if res.StatusCode != expectedStatus {
		t.Fatalf("msc2716.batchSendHistoricalMessages got %d HTTP status code from batch send response but want %d", res.StatusCode, expectedStatus)
	}

	chunkCount++

	return res
}

func getEventsFromBatchSendResponseBody(t *testing.T, body []byte) (eventIDs []string) {
	eventIDs = client.GetJSONFieldStringArray(t, body, "events")

	return eventIDs
}

func getNextChunkIdFromBatchSendResponseBody(t *testing.T, body []byte) (nextChunkID string) {
	nextChunkID = client.GetJSONFieldStr(t, body, "next_chunk_id")

	return nextChunkID
}
