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

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
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
	chunkEventType     = "org.matrix.msc2716.chunk"
	markerEventType    = "org.matrix.msc2716.marker"

	historicalContentField      = "org.matrix.msc2716.historical"
	nextChunkIDContentField     = "org.matrix.msc2716.next_chunk_id"
	markerInsertionContentField = "org.matrix.msc2716.marker.insertion"
)

var createRoomOpts = map[string]interface{}{
	"preset":       "public_chat",
	"name":         "the hangout spot",
	"room_version": "org.matrix.msc2716",
}

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
	// Register and join the virtual user
	ensureVirtualUserRegistered(t, as, virtualUserLocalpart)

	t.Run("parallel", func(t *testing.T) {
		// Test that the message events we insert between A and B come back in the correct order from /messages
		//
		// Final timeline output: ( [n] = historical chunk )
		// (oldest) A, B, [insertion, c, d, e, chunk] [insertion, f, g, h, chunk, insertion], I, J (newest)
		//                historical chunk 1          historical chunk 0
		t.Run("Backfilled historical events resolve with proper state in correct order", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createRoomOpts)
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

			// (oldest) A, B, [insertion, c, d, e, chunk] [insertion, f, g, h, chunk, insertion], I, J (newest)
			//                historical chunk 1          historical chunk 0
			if len(expectedEventIDOrder) != 15 {
				t.Fatalf("Expected eventID list should be length 15 but saw %d: %s", len(expectedEventIDOrder), expectedEventIDOrder)
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
						if isRelevantEvent(r) {
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

			roomID := as.CreateRoom(t, createRoomOpts)
			alice.JoinRoom(t, roomID, nil)

			// Create the "live" event we are going to insert our backfilled events next to
			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			// Register and join the other virtual users
			virtualUserID2 := "@ricky:hs1"
			ensureVirtualUserRegistered(t, as, "ricky")
			virtualUserID3 := "@carol:hs1"
			ensureVirtualUserRegistered(t, as, "carol")

			// Insert a backfilled event
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
					match.JSONCheckOffAllowUnwanted("chunk", makeInterfaceSlice(historicalEventIDs), func(r gjson.Result) interface{} {
						return r.Get("event_id").Str
					}, nil),
				},
			})
		})

		t.Run("Backfilled historical events with m.historical do not come down in an incremental sync", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createRoomOpts)
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

			roomID := as.CreateRoom(t, createRoomOpts)

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

			roomID := as.CreateRoom(t, createRoomOpts)
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

		t.Run("Historical messages are visible when joining on federated server - auto-generated base insertion event", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createRoomOpts)
			alice.JoinRoom(t, roomID, nil)

			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			// eventIDsAfter
			createMessagesInRoom(t, alice, roomID, 3)

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

			// Make sure all of the events have been backfilled
			fetchUntilMessagesResponseHas(t, remoteCharlie, roomID, func(ev gjson.Result) bool {
				if ev.Get("event_id").Str == eventIdBefore {
					return true
				}

				return false
			})

			messagesRes := remoteCharlie.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
				"dir":   []string{"b"},
				"limit": []string{"100"},
			}))

			must.MatchResponse(t, messagesRes, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONCheckOffAllowUnwanted("chunk", makeInterfaceSlice(historicalEventIDs), func(r gjson.Result) interface{} {
						return r.Get("event_id").Str
					}, nil),
				},
			})
		})

		t.Run("Historical messages are visible when joining on federated server - pre-made insertion event", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createRoomOpts)
			alice.JoinRoom(t, roomID, nil)

			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			// Create insertion event in the normal DAG
			chunkId := "mynextchunkid123"
			insertionEvent := b.Event{
				Type: insertionEventType,
				Content: map[string]interface{}{
					nextChunkIDContentField: chunkId,
					historicalContentField:  true,
				},
			}
			// We can't use as.SendEventSynced(...) because application services can't use the /sync API
			insertionSendRes := as.MustDoFunc(t, "PUT", []string{"_matrix", "client", "r0", "rooms", roomID, "send", insertionEvent.Type, "txn-m123"}, client.WithJSONBody(t, insertionEvent.Content))
			insertionSendBody := client.ParseJSON(t, insertionSendRes)
			insertionEventID := client.GetJSONFieldStr(t, insertionSendBody, "event_id")
			// Make sure the insertion event has reached the homeserver
			alice.SyncUntilTimelineHas(t, roomID, func(ev gjson.Result) bool {
				return ev.Get("event_id").Str == insertionEventID
			})

			// eventIDsAfter
			createMessagesInRoom(t, alice, roomID, 3)

			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				[]string{virtualUserID},
				roomID,
				eventIdBefore,
				timeAfterEventBefore,
				chunkId,
				2,
				// Status
				200,
			)
			batchSendResBody := client.ParseJSON(t, batchSendRes)
			historicalEventIDs := getEventsFromBatchSendResponseBody(t, batchSendResBody)

			// Join the room from a remote homeserver after the backfilled messages were sent
			remoteCharlie.JoinRoom(t, roomID, []string{"hs1"})

			// Make sure all of the events have been backfilled
			fetchUntilMessagesResponseHas(t, remoteCharlie, roomID, func(ev gjson.Result) bool {
				if ev.Get("event_id").Str == eventIdBefore {
					return true
				}

				return false
			})

			messagesRes := remoteCharlie.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
				"dir":   []string{"b"},
				"limit": []string{"100"},
			}))

			must.MatchResponse(t, messagesRes, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONCheckOffAllowUnwanted("chunk", makeInterfaceSlice(historicalEventIDs), func(r gjson.Result) interface{} {
						return r.Get("event_id").Str
					}, nil),
				},
			})
		})

		t.Run("Historical messages are visible when already joined on federated server", func(t *testing.T) {
			//t.Skip("Skipping until federation is implemented")
			t.Parallel()

			roomID := as.CreateRoom(t, createRoomOpts)
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
				// Limited so we can only see a portion of the latest messages
				"limit": []string{"5"},
			}))

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
			baseInsertionEventID := historicalEventIDs[len(historicalEventIDs)-1]

			// [1 insertion event + 2 historical events + 1 chunk event + 1 insertion event]
			if len(historicalEventIDs) != 5 {
				t.Fatalf("Expected eventID list should be length 5 but saw %d: %s", len(historicalEventIDs), historicalEventIDs)
			}

			beforeMarkerMessagesRes := remoteCharlie.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
				"dir":   []string{"b"},
				"limit": []string{"100"},
			}))
			beforeMarkerMesssageResBody := client.ParseJSON(t, beforeMarkerMessagesRes)
			eventDebugStringsFromBeforeMarkerResponse := getRelevantEventDebugStringsFromMessagesResponse(t, beforeMarkerMesssageResBody)
			// Since the original body can only be read once, create a new one from the body bytes we just read
			beforeMarkerMessagesRes.Body = ioutil.NopCloser(bytes.NewBuffer(beforeMarkerMesssageResBody))

			// Make sure the history isn't visible before we expect it to be there.
			// This is to avoid some bug in the homeserver using some unknown
			// mechanism to distribute the historical messages to other homeservers.
			must.MatchResponse(t, beforeMarkerMessagesRes, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONArrayEach("chunk", func(r gjson.Result) error {
						// Throw if we find one of the historical events in the message response
						for _, historicalEventID := range historicalEventIDs {
							if r.Get("event_id").Str == historicalEventID {
								return fmt.Errorf("Historical event (%s) found on remote homeserver before marker event was sent out\nmessage response (%d): %v\nhistoricalEventIDs (%d): %v", historicalEventID, len(eventDebugStringsFromBeforeMarkerResponse), eventDebugStringsFromBeforeMarkerResponse, len(historicalEventIDs), historicalEventIDs)
							}
						}

						return nil
					}),
				},
			})

			// Send the marker event
			sendMarkerAndEnsureBackfilled(t, as, remoteCharlie, roomID, baseInsertionEventID)

			remoteMessagesRes := remoteCharlie.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
				"dir":   []string{"b"},
				"limit": []string{"100"},
			}))

			// Make sure all of the historical messages are visible when we scrollback again
			must.MatchResponse(t, remoteMessagesRes, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONCheckOffAllowUnwanted("chunk", makeInterfaceSlice(historicalEventIDs), func(r gjson.Result) interface{} {
						return r.Get("event_id").Str
					}, nil),
				},
			})
		})

		t.Run("When messages have already been scrolled back through, new historical messages are visible in next scroll back on federated server", func(t *testing.T) {
			//t.Skip("Skipping until federation is implemented")
			t.Parallel()

			roomID := as.CreateRoom(t, createRoomOpts)
			alice.JoinRoom(t, roomID, nil)

			// Join the room from a remote homeserver before any backfilled messages are sent
			remoteCharlie.JoinRoom(t, roomID, []string{"hs1"})

			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			// eventIDsAfter
			createMessagesInRoom(t, alice, roomID, 3)

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
			baseInsertionEventID := historicalEventIDs[len(historicalEventIDs)-1]

			// [1 insertion event + 2 historical events + 1 chunk event + 1 insertion event]
			if len(historicalEventIDs) != 5 {
				t.Fatalf("Expected eventID list should be length 5 but saw %d: %s", len(historicalEventIDs), historicalEventIDs)
			}

			beforeMarkerMessagesRes := remoteCharlie.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
				"dir":   []string{"b"},
				"limit": []string{"100"},
			}))
			beforeMarkerMesssageResBody := client.ParseJSON(t, beforeMarkerMessagesRes)
			eventDebugStringsFromBeforeMarkerResponse := getRelevantEventDebugStringsFromMessagesResponse(t, beforeMarkerMesssageResBody)
			// Since the original body can only be read once, create a new one from the body bytes we just read
			beforeMarkerMessagesRes.Body = ioutil.NopCloser(bytes.NewBuffer(beforeMarkerMesssageResBody))
			// Make sure the history isn't visible before we expect it to be there.
			// This is to avoid some bug in the homeserver using some unknown
			// mechanism to distribute the historical messages to other homeservers.
			must.MatchResponse(t, beforeMarkerMessagesRes, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONArrayEach("chunk", func(r gjson.Result) error {
						// Throw if we find one of the historical events in the message response
						for _, historicalEventID := range historicalEventIDs {
							if r.Get("event_id").Str == historicalEventID {
								return fmt.Errorf("Historical event (%s) found on remote homeserver before marker event was sent out\nmessage response (%d): %v\nhistoricalEventIDs (%d): %v", historicalEventID, len(eventDebugStringsFromBeforeMarkerResponse), eventDebugStringsFromBeforeMarkerResponse, len(historicalEventIDs), historicalEventIDs)
							}
						}
						return nil
					}),
				},
			})

			// Send the marker event
			sendMarkerAndEnsureBackfilled(t, as, remoteCharlie, roomID, baseInsertionEventID)

			remoteMessagesRes := remoteCharlie.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
				"dir":   []string{"b"},
				"limit": []string{"100"},
			}))

			// Make sure all of the historical messages are visible when we scrollback again
			must.MatchResponse(t, remoteMessagesRes, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONCheckOffAllowUnwanted("chunk", makeInterfaceSlice(historicalEventIDs), func(r gjson.Result) interface{} {
						return r.Get("event_id").Str
					}, nil),
				},
			})
		})
	})
}

func makeInterfaceSlice(slice []string) []interface{} {
	interfaceSlice := make([]interface{}, len(slice))
	for i := range slice {
		interfaceSlice[i] = slice[i]
	}

	return interfaceSlice
}

func reversed(in []string) []string {
	out := make([]string, len(in))
	for i := 0; i < len(in); i++ {
		out[i] = in[len(in)-i-1]
	}
	return out
}

func fetchUntilMessagesResponseHas(t *testing.T, c *client.CSAPI, roomID string, check func(gjson.Result) bool) {
	t.Helper()
	start := time.Now()
	checkCounter := 0
	for {
		if time.Since(start) > c.SyncUntilTimeout {
			t.Fatalf("fetchMessagesUntilResponseHas timed out. Called check function %d times", checkCounter)
		}

		messagesRes := c.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
			"dir":   []string{"b"},
			"limit": []string{"100"},
		}))
		messsageResBody := client.ParseJSON(t, messagesRes)
		wantKey := "chunk"
		keyRes := gjson.GetBytes(messsageResBody, wantKey)
		if !keyRes.Exists() {
			t.Fatalf("missing key '%s'", wantKey)
		}
		if !keyRes.IsArray() {
			t.Fatalf("key '%s' is not an array (was %s)", wantKey, keyRes.Type)
		}

		events := keyRes.Array()
		for _, ev := range events {
			if check(ev) {
				return
			}
		}

		checkCounter++
		// Add a slight delay so we don't hammmer the messages endpoint
		time.Sleep(500 * time.Millisecond)
	}
}

func isRelevantEvent(r gjson.Result) bool {
	return len(r.Get("content").Get("body").Str) > 0 ||
		r.Get("type").Str == insertionEventType ||
		r.Get("type").Str == chunkEventType ||
		r.Get("type").Str == markerEventType
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
		if isRelevantEvent(r) {
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

func sendMarkerAndEnsureBackfilled(t *testing.T, as *client.CSAPI, c *client.CSAPI, roomID, insertionEventID string) {
	// Send a marker event to let all of the homeservers know about the
	// insertion point where all of the historical messages are at
	markerEvent := b.Event{
		Type: markerEventType,
		Content: map[string]interface{}{
			markerInsertionContentField: insertionEventID,
		},
	}
	// We can't use as.SendEventSynced(...) because application services can't use the /sync API
	markerSendRes := as.MustDoFunc(t, "PUT", []string{"_matrix", "client", "r0", "rooms", roomID, "send", markerEvent.Type, "txn-m123"}, client.WithJSONBody(t, markerEvent.Content))
	markerSendBody := client.ParseJSON(t, markerSendRes)
	markerEventID := client.GetJSONFieldStr(t, markerSendBody, "event_id")

	// Make sure the marker event has reached the remote homeserver
	c.SyncUntilTimelineHas(t, roomID, func(ev gjson.Result) bool {
		return ev.Get("event_id").Str == markerEventID
	})

	// Make sure all of the base insertion event has been backfilled
	// after the marker was received
	fetchUntilMessagesResponseHas(t, c, roomID, func(ev gjson.Result) bool {
		if ev.Get("event_id").Str == insertionEventID {
			return true
		}

		return false
	})
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
