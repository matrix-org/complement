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
	batchEventType     = "org.matrix.msc2716.batch"
	markerEventType    = "org.matrix.msc2716.marker"

	historicalContentField      = "org.matrix.msc2716.historical"
	nextBatchIDContentField     = "org.matrix.msc2716.next_batch_id"
	markerInsertionContentField = "org.matrix.msc2716.marker.insertion"
)

var createPublicRoomOpts = map[string]interface{}{
	"preset":       "public_chat",
	"name":         "the hangout spot",
	"room_version": "org.matrix.msc2716v3",
}

var createPrivateRoomOpts = map[string]interface{}{
	"preset":       "private_chat",
	"name":         "the hangout spot",
	"room_version": "org.matrix.msc2716v3",
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
		// Final timeline output: ( [n] = historical batch )
		// (oldest) A, B, [insertion, c, d, e, batch] [insertion, f, g, h, batch, insertion], I, J (newest)
		//                historical batch 1          historical batch 0
		t.Run("Backfilled historical events resolve with proper state in correct order", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createPublicRoomOpts)
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

			// Insert the most recent batch of backfilled history
			insertTime0 := timeAfterEventBefore.Add(timeBetweenMessages * 3)
			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				roomID,
				eventIdBefore,
				"",
				createJoinStateEventsForBackfillRequest([]string{virtualUserID}, insertTime0),
				createMessageEventsForBackfillRequest([]string{virtualUserID}, insertTime0, 3),
				// Status
				200,
			)
			batchSendResBody0 := client.ParseJSON(t, batchSendRes)
			insertionEventID0 := client.GetJSONFieldStr(t, batchSendResBody0, "insertion_event_id")
			historicalEventIDs0 := client.GetJSONFieldStringArray(t, batchSendResBody0, "event_ids")
			batchEventID0 := client.GetJSONFieldStr(t, batchSendResBody0, "batch_event_id")
			baseInsertionEventID0 := client.GetJSONFieldStr(t, batchSendResBody0, "base_insertion_event_id")
			nextBatchID0 := client.GetJSONFieldStr(t, batchSendResBody0, "next_batch_id")

			// Insert another older batch of backfilled history from the same user.
			// Make sure the meta data and joins still work on the subsequent batch
			insertTime1 := timeAfterEventBefore
			batchSendRes1 := batchSendHistoricalMessages(
				t,
				as,
				roomID,
				eventIdBefore,
				nextBatchID0,
				createJoinStateEventsForBackfillRequest([]string{virtualUserID}, insertTime1),
				createMessageEventsForBackfillRequest([]string{virtualUserID}, insertTime1, 3),
				// Status
				200,
			)
			batchSendResBody1 := client.ParseJSON(t, batchSendRes1)
			insertionEventID1 := client.GetJSONFieldStr(t, batchSendResBody1, "insertion_event_id")
			historicalEventIDs1 := client.GetJSONFieldStringArray(t, batchSendResBody1, "event_ids")
			batchEventID1 := client.GetJSONFieldStr(t, batchSendResBody1, "batch_event_id")

			var expectedEventIDOrder []string
			expectedEventIDOrder = append(expectedEventIDOrder, eventIDsBefore...)
			expectedEventIDOrder = append(expectedEventIDOrder, insertionEventID1)
			expectedEventIDOrder = append(expectedEventIDOrder, historicalEventIDs1...)
			expectedEventIDOrder = append(expectedEventIDOrder, batchEventID1)
			expectedEventIDOrder = append(expectedEventIDOrder, insertionEventID0)
			expectedEventIDOrder = append(expectedEventIDOrder, historicalEventIDs0...)
			expectedEventIDOrder = append(expectedEventIDOrder, batchEventID0)
			expectedEventIDOrder = append(expectedEventIDOrder, baseInsertionEventID0)
			expectedEventIDOrder = append(expectedEventIDOrder, eventIDsAfter...)
			// Order events from newest to oldest
			expectedEventIDOrder = reversed(expectedEventIDOrder)

			// (oldest) A, B, [insertion, c, d, e, batch] [insertion, f, g, h, batch, insertion], I, J (newest)
			//                historical batch 1          historical batch 0
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

		t.Run("Backfilled historical events from multiple users in the same batch", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createPublicRoomOpts)
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

			virtualUserList := []string{virtualUserID, virtualUserID2, virtualUserID3}

			// Insert a backfilled event
			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				roomID,
				eventIdBefore,
				"",
				createJoinStateEventsForBackfillRequest(virtualUserList, timeAfterEventBefore),
				createMessageEventsForBackfillRequest(virtualUserList, timeAfterEventBefore, 3),
				// Status
				200,
			)
			batchSendResBody := client.ParseJSON(t, batchSendRes)
			historicalEventIDs := client.GetJSONFieldStringArray(t, batchSendResBody, "event_ids")

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

		t.Run("Backfilled historical events do not come down in an incremental sync", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createPublicRoomOpts)
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
				roomID,
				eventIdBefore,
				"",
				createJoinStateEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore),
				createMessageEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore, 1),
				// Status
				200,
			)
			batchSendResBody := client.ParseJSON(t, batchSendRes)
			historicalEventIDs := client.GetJSONFieldStringArray(t, batchSendResBody, "event_ids")
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

		t.Run("Batch send endpoint only returns state events that we passed in via state_events_at_start", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createPublicRoomOpts)
			alice.JoinRoom(t, roomID, nil)

			// Create the "live" event we are going to insert our backfilled events next to
			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			// Insert a backfilled event
			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				roomID,
				eventIdBefore,
				"",
				createJoinStateEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore),
				createMessageEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore, 1),
				// Status
				200,
			)
			batchSendResBody := client.ParseJSON(t, batchSendRes)
			stateEventIDs := client.GetJSONFieldStringArray(t, batchSendResBody, "state_event_ids")

			// We only expect 1 state event to be returned because we only passed in 1
			// event into `?state_events_at_start`
			if len(stateEventIDs) != 1 {
				t.Fatalf("Expected only 1 state event to be returned but received %d: %v", len(stateEventIDs), stateEventIDs)
			}
		})

		t.Run("Unrecognised prev_event ID will throw an error", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createPublicRoomOpts)

			insertTime := time.Now()
			batchSendHistoricalMessages(
				t,
				as,
				roomID,
				"$some-non-existant-event-id",
				"",
				createJoinStateEventsForBackfillRequest([]string{virtualUserID}, insertTime),
				createMessageEventsForBackfillRequest([]string{virtualUserID}, insertTime, 1),
				// Status
				// TODO: Seems like this makes more sense as a 404
				// But the current Synapse code around unknown prev events will throw ->
				// `403: No create event in auth events`
				403,
			)
		})

		t.Run("Unrecognised batch_id will throw an error", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createPublicRoomOpts)
			alice.JoinRoom(t, roomID, nil)

			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			batchSendHistoricalMessages(
				t,
				as,
				roomID,
				eventIdBefore,
				"XXX_DOES_NOT_EXIST_BATCH_ID",
				createJoinStateEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore),
				createMessageEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore, 1),
				// Status
				400,
			)
		})

		t.Run("Normal users aren't allowed to backfill messages", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createPublicRoomOpts)
			alice.JoinRoom(t, roomID, nil)

			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			batchSendHistoricalMessages(
				t,
				alice,
				roomID,
				eventIdBefore,
				"",
				createJoinStateEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore),
				createMessageEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore, 1),
				// Status
				// Normal user alice should not be able to backfill messages
				403,
			)
		})

		t.Run("TODO: Trying to send insertion event with same `next_batch_id` will reject", func(t *testing.T) {
			t.Skip("Skipping until implemented")
			// (room_id, next_batch_id) should be unique
		})

		t.Run("Should be able to backfill into private room", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createPrivateRoomOpts)
			as.InviteRoom(t, roomID, alice.UserID)
			alice.JoinRoom(t, roomID, nil)

			// Create the "live" event we are going to insert our backfilled events next to
			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			var stateEvents []map[string]interface{}
			stateEvents = append(stateEvents, createInviteStateEventsForBackfillRequest(as.UserID, []string{virtualUserID}, timeAfterEventBefore)...)
			stateEvents = append(stateEvents, createJoinStateEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore)...)

			// Insert a backfilled event
			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				roomID,
				eventIdBefore,
				"",
				stateEvents,
				createMessageEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore, 3),
				// Status
				200,
			)
			batchSendResBody := client.ParseJSON(t, batchSendRes)
			historicalEventIDs := client.GetJSONFieldStringArray(t, batchSendResBody, "event_ids")

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

		t.Run("TODO: Test if historical avatar/display name set back in time are picked up on historical messages", func(t *testing.T) {
			t.Skip("Skipping until implemented")
			// TODO: Try adding avatar and displayName and see if historical messages get this info
		})

		t.Run("TODO: What happens when you point multiple batches at the same insertion event?", func(t *testing.T) {
			t.Skip("Skipping until implemented")
		})

		t.Run("Historical messages are visible when joining on federated server - auto-generated base insertion event", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createPublicRoomOpts)
			alice.JoinRoom(t, roomID, nil)

			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			// eventIDsAfter
			createMessagesInRoom(t, alice, roomID, 3)

			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				roomID,
				eventIdBefore,
				"",
				createJoinStateEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore),
				createMessageEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore, 2),
				// Status
				200,
			)
			batchSendResBody := client.ParseJSON(t, batchSendRes)
			historicalEventIDs := client.GetJSONFieldStringArray(t, batchSendResBody, "event_ids")

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

			roomID := as.CreateRoom(t, createPublicRoomOpts)
			alice.JoinRoom(t, roomID, nil)

			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			// Create insertion event in the normal DAG
			batchID := "mynextBatchID123"
			insertionEvent := b.Event{
				Type: insertionEventType,
				Content: map[string]interface{}{
					nextBatchIDContentField: batchID,
					historicalContentField:  true,
				},
			}
			// We can't use as.SendEventSynced(...) because application services can't use the /sync API
			txnId := getTxnID("sendInsertionAndEnsureBackfilled-txn")
			insertionSendRes := as.MustDoFunc(t, "PUT", []string{"_matrix", "client", "r0", "rooms", roomID, "send", insertionEvent.Type, txnId}, client.WithJSONBody(t, insertionEvent.Content))
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
				roomID,
				eventIdBefore,
				batchID,
				createJoinStateEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore),
				createMessageEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore, 2),
				// Status
				200,
			)
			batchSendResBody := client.ParseJSON(t, batchSendRes)
			historicalEventIDs := client.GetJSONFieldStringArray(t, batchSendResBody, "event_ids")

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
			t.Parallel()

			roomID := as.CreateRoom(t, createPublicRoomOpts)
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

			numMessagesSent := 2
			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				roomID,
				eventIdBefore,
				"",
				createJoinStateEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore),
				createMessageEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore, numMessagesSent),
				// Status
				200,
			)
			batchSendResBody := client.ParseJSON(t, batchSendRes)
			historicalEventIDs := client.GetJSONFieldStringArray(t, batchSendResBody, "event_ids")
			baseInsertionEventID := client.GetJSONFieldStr(t, batchSendResBody, "base_insertion_event_id")

			if len(historicalEventIDs) != numMessagesSent {
				t.Fatalf("Expected %d event_ids in the response that correspond to the %d events we sent in the request but saw %d: %s", numMessagesSent, numMessagesSent, len(historicalEventIDs), historicalEventIDs)
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
			t.Parallel()

			roomID := as.CreateRoom(t, createPublicRoomOpts)
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
			numMessagesSent := 2
			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				roomID,
				eventIdBefore,
				"",
				createJoinStateEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore),
				createMessageEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore, numMessagesSent),
				// Status
				200,
			)
			batchSendResBody := client.ParseJSON(t, batchSendRes)
			historicalEventIDs := client.GetJSONFieldStringArray(t, batchSendResBody, "event_ids")
			baseInsertionEventID := client.GetJSONFieldStr(t, batchSendResBody, "base_insertion_event_id")

			if len(historicalEventIDs) != numMessagesSent {
				t.Fatalf("Expected %d event_ids in the response that correspond to the %d events we sent in the request but saw %d: %s", numMessagesSent, numMessagesSent, len(historicalEventIDs), historicalEventIDs)
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

		t.Run("Existing room versions", func(t *testing.T) {
			createUnsupportedMSC2716RoomOpts := map[string]interface{}{
				"preset": "public_chat",
				"name":   "the hangout spot",
				// v6 is an existing room version that does not support MSC2716
				"room_version": "6",
			}

			t.Run("Room creator can send MSC2716 events", func(t *testing.T) {
				t.Parallel()

				roomID := as.CreateRoom(t, createUnsupportedMSC2716RoomOpts)
				alice.JoinRoom(t, roomID, nil)

				// Create the "live" event we are going to insert our backfilled events next to
				eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
				eventIdBefore := eventIDsBefore[0]
				timeAfterEventBefore := time.Now()

				// Create eventIDsAfter to avoid the "No forward extremities left!" 500 error from Synapse
				createMessagesInRoom(t, alice, roomID, 2)

				// Insert a backfilled event
				batchSendRes := batchSendHistoricalMessages(
					t,
					as,
					roomID,
					eventIdBefore,
					"",
					createJoinStateEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore),
					createMessageEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore, 1),
					// Status
					200,
				)
				batchSendResBody := client.ParseJSON(t, batchSendRes)
				historicalEventIDs := client.GetJSONFieldStringArray(t, batchSendResBody, "event_ids")
				nextBatchID := client.GetJSONFieldStr(t, batchSendResBody, "next_batch_id")

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

				// Now try to do a subsequent batch send. This will make sure
				// that insertion events are stored/tracked and can be matched up in the next batch
				batchSendHistoricalMessages(
					t,
					as,
					roomID,
					eventIdBefore,
					nextBatchID,
					createJoinStateEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore),
					createMessageEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore, 1),
					// Status
					200,
				)
			})

			t.Run("Not allowed to redact MSC2716 insertion, batch, marker events", func(t *testing.T) {
				t.Parallel()

				roomID := as.CreateRoom(t, createUnsupportedMSC2716RoomOpts)
				alice.JoinRoom(t, roomID, nil)

				// Create the "live" event we are going to insert our backfilled events next to
				eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
				eventIdBefore := eventIDsBefore[0]
				timeAfterEventBefore := time.Now()

				// Insert a backfilled event
				batchSendRes := batchSendHistoricalMessages(
					t,
					as,
					roomID,
					eventIdBefore,
					"",
					createJoinStateEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore),
					createMessageEventsForBackfillRequest([]string{virtualUserID}, timeAfterEventBefore, 1),
					// Status
					200,
				)
				batchSendResBody := client.ParseJSON(t, batchSendRes)
				insertionEventID := client.GetJSONFieldStr(t, batchSendResBody, "insertion_event_id")
				batchEventID := client.GetJSONFieldStr(t, batchSendResBody, "batch_event_id")
				baseInsertionEventID := client.GetJSONFieldStr(t, batchSendResBody, "base_insertion_event_id")

				// Send the marker event
				markerEventID := sendMarkerAndEnsureBackfilled(t, as, alice, roomID, baseInsertionEventID)

				redactEventID(t, alice, roomID, insertionEventID, 403)
				redactEventID(t, alice, roomID, batchEventID, 403)
				redactEventID(t, alice, roomID, markerEventID, 403)
			})
		})
	})
}

var txnCounter int = 0

func getTxnID(prefix string) (txnID string) {
	txnId := fmt.Sprintf("%s-%d", prefix, txnCounter)

	txnCounter++

	return txnId
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
			t.Fatalf("fetchUntilMessagesResponseHas timed out. Called check function %d times", checkCounter)
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
		r.Get("type").Str == batchEventType ||
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

func sendMarkerAndEnsureBackfilled(t *testing.T, as *client.CSAPI, c *client.CSAPI, roomID, insertionEventID string) (markerEventID string) {
	t.Helper()

	// Send a marker event to let all of the homeservers know about the
	// insertion point where all of the historical messages are at
	markerEvent := b.Event{
		Type: markerEventType,
		Content: map[string]interface{}{
			markerInsertionContentField: insertionEventID,
		},
	}
	// We can't use as.SendEventSynced(...) because application services can't use the /sync API
	txnId := getTxnID("sendMarkerAndEnsureBackfilled-txn")
	markerSendRes := as.MustDoFunc(t, "PUT", []string{"_matrix", "client", "r0", "rooms", roomID, "send", markerEvent.Type, txnId}, client.WithJSONBody(t, markerEvent.Content))
	markerSendBody := client.ParseJSON(t, markerSendRes)
	markerEventID = client.GetJSONFieldStr(t, markerSendBody, "event_id")

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

	return markerEventID
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

func createInviteStateEventsForBackfillRequest(
	invitedByUserID string,
	virtualUserIDs []string,
	insertTime time.Time,
) []map[string]interface{} {
	// Timestamp in milliseconds
	insertOriginServerTs := uint64(insertTime.UnixNano() / int64(time.Millisecond))

	stateEvents := make([]map[string]interface{}, len(virtualUserIDs))
	for i, virtualUserID := range virtualUserIDs {
		inviteEvent := map[string]interface{}{
			"type":             "m.room.member",
			"sender":           invitedByUserID,
			"origin_server_ts": insertOriginServerTs,
			"content": map[string]interface{}{
				"membership": "invite",
			},
			"state_key": virtualUserID,
		}

		stateEvents[i] = inviteEvent
	}

	return stateEvents
}

func createJoinStateEventsForBackfillRequest(
	virtualUserIDs []string,
	insertTime time.Time,
) []map[string]interface{} {
	// Timestamp in milliseconds
	insertOriginServerTs := uint64(insertTime.UnixNano() / int64(time.Millisecond))

	stateEvents := make([]map[string]interface{}, len(virtualUserIDs))
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

		stateEvents[i] = joinEvent
	}

	return stateEvents
}

func createMessageEventsForBackfillRequest(
	virtualUserIDs []string,
	insertTime time.Time,
	count int,
) []map[string]interface{} {
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
				"body":                 fmt.Sprintf("Historical %d (batch=%d)", i, batchCount),
				historicalContentField: true,
			},
		}

		evs[i] = newEvent
	}

	return evs
}

func redactEventID(t *testing.T, c *client.CSAPI, roomID, eventID string, expectedStatus int) {
	t.Helper()

	txnID := getTxnID("redactEventID-txn")
	redactionRes := c.DoFunc(
		t,
		"PUT",
		[]string{"_matrix", "client", "r0", "rooms", roomID, "redact", eventID, txnID},
		client.WithJSONBody(t, map[string]interface{}{"reason": "chaos"}),
		client.WithContentType("application/json"),
	)
	redactionResBody := client.ParseJSON(t, redactionRes)
	redactionResErrcode := client.GetJSONFieldStr(t, redactionResBody, "error")
	redactionResError := client.GetJSONFieldStr(t, redactionResBody, "errcode")

	if redactionRes.StatusCode != expectedStatus {
		t.Fatalf("msc2716.redactEventID: Expected redaction response to be %d but received %d -> %s: %s", expectedStatus, redactionRes.StatusCode, redactionResErrcode, redactionResError)
	}
}

var batchCount int64 = 0

func batchSendHistoricalMessages(
	t *testing.T,
	c *client.CSAPI,
	roomID string,
	insertAfterEventId string,
	batchID string,
	stateEventsAtStart []map[string]interface{},
	events []map[string]interface{},
	expectedStatus int,
) (res *http.Response) {
	t.Helper()

	query := make(url.Values, 2)
	query.Add("prev_event_id", insertAfterEventId)
	// If provided, connect the batch to the last insertion point
	if batchID != "" {
		query.Add("batch_id", batchID)
	}

	res = c.DoFunc(
		t,
		"POST",
		[]string{"_matrix", "client", "unstable", "org.matrix.msc2716", "rooms", roomID, "batch_send"},
		client.WithJSONBody(t, map[string]interface{}{
			"events":                events,
			"state_events_at_start": stateEventsAtStart,
		}),
		client.WithContentType("application/json"),
		client.WithQueries(query),
	)

	if res.StatusCode != expectedStatus {
		t.Fatalf("msc2716.batchSendHistoricalMessages got %d HTTP status code from batch send response but want %d", res.StatusCode, expectedStatus)
	}

	batchCount++

	return res
}
