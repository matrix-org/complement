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
	"strings"
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
	"room_version": "org.matrix.msc2716v4",
}

var createPrivateRoomOpts = map[string]interface{}{
	"preset":       "private_chat",
	"name":         "the hangout spot",
	"room_version": "org.matrix.msc2716v4",
}

func TestImportHistoricalMessages(t *testing.T) {
	deployment := Deploy(t, b.BlueprintHSWithApplicationService)
	defer deployment.Destroy(t)

	// Create the application service bridge user that is able to import historical messages
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
		// Test that the historical message events we import between A and B
		// come back in the correct order from /messages.
		//
		// Final timeline output: ( [n] = historical batch )
		// (oldest) A, B, [insertion, c, d, e, batch] [insertion, f, g, h, batch], baseInsertion, I, J (newest)
		//                historical batch 1          historical batch 0
		t.Run("Historical events resolve in the correct order", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createPublicRoomOpts)
			alice.JoinRoom(t, roomID, nil)

			// Create some normal messages in the timeline. We're creating them in
			// two batches so we can create some time in between where we are going
			// to import.
			//
			// Create the first batch including the "live" event we are going to
			// import our historical events next to.
			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 2)
			eventIdBefore := eventIDsBefore[len(eventIDsBefore)-1]
			timeAfterEventBefore := time.Now()

			// wait X number of ms to ensure that the timestamp changes enough for
			// each of the historical messages we try to import later
			numHistoricalMessages := 6
			time.Sleep(time.Duration(numHistoricalMessages) * timeBetweenMessages)

			// Create the second batch of events.
			// This will also fill up the buffer so we have to scrollback to the
			// inserted history later.
			eventIDsAfter := createMessagesInRoom(t, alice, roomID, 2)

			// Insert the most recent batch of historical messages
			insertTime0 := timeAfterEventBefore.Add(timeBetweenMessages * 3)
			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				roomID,
				eventIdBefore,
				"",
				createJoinStateEventsForBatchSendRequest([]string{virtualUserID}, insertTime0),
				createMessageEventsForBatchSendRequest([]string{virtualUserID}, insertTime0, 3),
				// Status
				200,
			)
			batchSendResBody0 := client.ParseJSON(t, batchSendRes)
			insertionEventID0 := client.GetJSONFieldStr(t, batchSendResBody0, "insertion_event_id")
			historicalEventIDs0 := client.GetJSONFieldStringArray(t, batchSendResBody0, "event_ids")
			batchEventID0 := client.GetJSONFieldStr(t, batchSendResBody0, "batch_event_id")
			baseInsertionEventID0 := client.GetJSONFieldStr(t, batchSendResBody0, "base_insertion_event_id")
			nextBatchID0 := client.GetJSONFieldStr(t, batchSendResBody0, "next_batch_id")

			// Insert another older batch of historical messages from the same user.
			// Make sure the meta data and joins still work on the subsequent batch
			insertTime1 := timeAfterEventBefore
			batchSendRes1 := batchSendHistoricalMessages(
				t,
				as,
				roomID,
				eventIdBefore,
				nextBatchID0,
				createJoinStateEventsForBatchSendRequest([]string{virtualUserID}, insertTime1),
				createMessageEventsForBatchSendRequest([]string{virtualUserID}, insertTime1, 3),
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

			must.MatchResponse(t, messagesRes, match.HTTPResponse{
				JSON: []match.JSON{
					matcherJSONEventIDArrayInOrder("chunk",
						expectedEventIDOrder,
						relevantToScrollbackEventFilter,
					),
				},
			})
		})

		t.Run("Historical events from multiple users in the same batch", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createPublicRoomOpts)
			alice.JoinRoom(t, roomID, nil)

			// Create the "live" event we are going to insert our historical events next to
			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			// Register and join the other virtual users
			virtualUserID2 := "@ricky:hs1"
			ensureVirtualUserRegistered(t, as, "ricky")
			virtualUserID3 := "@carol:hs1"
			ensureVirtualUserRegistered(t, as, "carol")

			virtualUserList := []string{virtualUserID, virtualUserID2, virtualUserID3}

			// Import a historical event
			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				roomID,
				eventIdBefore,
				"",
				createJoinStateEventsForBatchSendRequest(virtualUserList, timeAfterEventBefore),
				createMessageEventsForBatchSendRequest(virtualUserList, timeAfterEventBefore, 3),
				// Status
				200,
			)
			validateBatchSendRes(t, as, roomID, batchSendRes, false)
		})

		t.Run("Historical events from batch_send do not come down in an incremental sync", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createPublicRoomOpts)
			alice.JoinRoom(t, roomID, nil)

			// Create the "live" event we are going to insert our historical events next to
			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			// Create some "live" events to saturate and fill up the /sync response
			createMessagesInRoom(t, alice, roomID, 5)

			// Get a /sync `since` pagination token we can try paginating from later
			// on
			_, since := alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

			// Import a historical event
			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				roomID,
				eventIdBefore,
				"",
				createJoinStateEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore),
				createMessageEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore, 1),
				// Status
				200,
			)
			batchSendResBody := client.ParseJSON(t, batchSendRes)
			historicalEventIDs := client.GetJSONFieldStringArray(t, batchSendResBody, "event_ids")
			historicalStateEventIDs := client.GetJSONFieldStringArray(t, batchSendResBody, "state_event_ids")

			// This is just a dummy event we search for after the historicalEventIDs/historicalStateEventIDs
			eventIDsAfterHistoricalImport := createMessagesInRoom(t, alice, roomID, 1)
			eventIDAfterHistoricalImport := eventIDsAfterHistoricalImport[0]

			// Sync from before we did any batch sending until we find the
			// eventIDAfterHistoricalImport. If we're able to see
			// eventIDAfterHistoricalImport without any the
			// historicalEventIDs/historicalStateEventIDs in between, we're probably
			// safe to assume it won't sync.
			alice.MustSyncUntil(t, client.SyncReq{Since: since}, client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
				if includes(r.Get("event_id").Str, historicalEventIDs) || includes(r.Get("event_id").Str, historicalStateEventIDs) {
					t.Fatalf("We should not see the %s historical event in /sync response but it was present", r.Get("event_id").Str)
				}

				return r.Get("event_id").Str == eventIDAfterHistoricalImport
			}))
		})

		t.Run("Batch send endpoint only returns state events that we passed in via state_events_at_start", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createPublicRoomOpts)
			alice.JoinRoom(t, roomID, nil)

			// Create the "live" event we are going to import our historical events next to
			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			// Import a historical event
			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				roomID,
				eventIdBefore,
				"",
				createJoinStateEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore),
				createMessageEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore, 1),
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
				createJoinStateEventsForBatchSendRequest([]string{virtualUserID}, insertTime),
				createMessageEventsForBatchSendRequest([]string{virtualUserID}, insertTime, 1),
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
				createJoinStateEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore),
				createMessageEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore, 1),
				// Status
				400,
			)
		})

		t.Run("Duplicate next_batch_id on insertion event will be rejected", func(t *testing.T) {
			t.Parallel()

			// Alice created the room and is the room creator/admin to be able to
			// send historical events.
			//
			// We're using Alice over the application service so we can easily use
			// SendEventSynced since application services can't use /sync.
			roomID := alice.CreateRoom(t, createPublicRoomOpts)

			alice.SendEventSynced(t, roomID, b.Event{
				Type: insertionEventType,
				Content: map[string]interface{}{
					nextBatchIDContentField: "same",
					historicalContentField:  true,
				},
			})

			txnId := getTxnID("duplicateinsertion-txn")
			res := alice.DoFunc(t, "PUT", []string{"_matrix", "client", "r0", "rooms", roomID, "send", insertionEventType, txnId}, client.WithJSONBody(t, map[string]interface{}{
				nextBatchIDContentField: "same",
				historicalContentField:  true,
			}))

			// We expect the send request for the duplicate insertion event to fail
			expectedStatus := 400
			if res.StatusCode != expectedStatus {
				t.Fatalf("Expected HTTP Status to be %d but received %d", expectedStatus, res.StatusCode)
			}
		})

		t.Run("Normal users aren't allowed to batch send historical messages", func(t *testing.T) {
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
				createJoinStateEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore),
				createMessageEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore, 1),
				// Status
				// Normal user alice should not be able to batch send historical messages
				403,
			)
		})

		t.Run("TODO: Trying to send insertion event with same `next_batch_id` will reject", func(t *testing.T) {
			t.Skip("Skipping until implemented")
			// (room_id, next_batch_id) should be unique
		})

		t.Run("Should be able to batch send historical messages into private room", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createPrivateRoomOpts)
			as.InviteRoom(t, roomID, alice.UserID)
			alice.JoinRoom(t, roomID, nil)

			// Create the "live" event we are going to import our historical events next to
			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			var stateEvents []map[string]interface{}
			stateEvents = append(stateEvents, createInviteStateEventsForBacthSendRequest(as.UserID, []string{virtualUserID}, timeAfterEventBefore)...)
			stateEvents = append(stateEvents, createJoinStateEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore)...)

			// Import a historical event
			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				roomID,
				eventIdBefore,
				"",
				stateEvents,
				createMessageEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore, 3),
				// Status
				200,
			)
			validateBatchSendRes(
				t,
				as,
				roomID,
				batchSendRes,
				// We can't validate the state in this case because the invite event
				// won't be resolved in `/messages` state field (i.e. only the member
				// event is needed to auth those events)
				false,
			)
		})

		t.Run("should resolve member state events for historical events", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createPublicRoomOpts)
			alice.JoinRoom(t, roomID, nil)

			// Create the "live" event we are going to insert our backfilled events next to
			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			// eventIDsAfter
			createMessagesInRoom(t, alice, roomID, 2)

			// Import a batch of historical events
			batchSendRes0 := batchSendHistoricalMessages(
				t,
				as,
				roomID,
				eventIdBefore,
				"",
				createJoinStateEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore),
				createMessageEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore, 4),
				// Status
				200,
			)
			validateBatchSendRes(
				t,
				as,
				roomID,
				batchSendRes0,
				// Validate the state
				true,
			)
			batchSendResBody0 := client.ParseJSON(t, batchSendRes0)
			nextBatchID0 := client.GetJSONFieldStr(t, batchSendResBody0, "next_batch_id")

			// Import another older batch of history from the same user.
			// Make sure the meta data and joins still work on the subsequent batch
			batchSendRes1 := batchSendHistoricalMessages(
				t,
				as,
				roomID,
				eventIdBefore,
				nextBatchID0,
				createJoinStateEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore),
				createMessageEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore, 4),
				// Status
				200,
			)
			validateBatchSendRes(
				t,
				as,
				roomID,
				batchSendRes1,
				// Validate the state
				true,
			)
		})

		t.Run("Should be able to send a batch without any state_events_at_start - user already joined in the current room state", func(t *testing.T) {
			t.Parallel()

			roomID := as.CreateRoom(t, createPublicRoomOpts)
			alice.JoinRoom(t, roomID, nil)

			// Create the "live" event we are going to import our historical events next to
			eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
			eventIdBefore := eventIDsBefore[0]
			timeAfterEventBefore := time.Now()

			// Import a historical event
			batchSendRes := batchSendHistoricalMessages(
				t,
				as,
				roomID,
				eventIdBefore,
				"",
				// The key thing we're testing here is that we can still batch send
				// messages when no `state_events_at_start` are given
				make([]map[string]interface{}, 0),
				createMessageEventsForBatchSendRequest([]string{alice.UserID}, timeAfterEventBefore, 3),
				// Status
				200,
			)

			validateBatchSendRes(
				t,
				as,
				roomID,
				batchSendRes,
				false,
			)
		})

		t.Run("TODO: What happens when you point multiple batches at the same insertion event?", func(t *testing.T) {
			t.Skip("Skipping until implemented")
		})

		t.Run("Federation", func(t *testing.T) {
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
					createJoinStateEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore),
					createMessageEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore, 2),
					// Status
					200,
				)
				batchSendResBody := client.ParseJSON(t, batchSendRes)
				historicalEventIDs := client.GetJSONFieldStringArray(t, batchSendResBody, "event_ids")
				baseInsertionEventID := client.GetJSONFieldStr(t, batchSendResBody, "base_insertion_event_id")

				// Send the marker event which lets remote homeservers know there are
				// some historical messages back at the given insertion event.
				sendMarkerAndEnsureBackfilled(t, as, alice, roomID, baseInsertionEventID)

				// Join the room from a remote homeserver after the historical messages were sent
				remoteCharlie.JoinRoom(t, roomID, []string{"hs1"})

				// Make sure all of the events have been backfilled
				fetchUntilMessagesResponseHas(t, remoteCharlie, roomID, func(ev gjson.Result) bool {
					if ev.Get("event_id").Str == eventIdBefore {
						return true
					}

					return false
				})

				// FIXME: In the future, we should probably replace the following logic
				// with `validateBatchSendRes` to re-use and have some more robust
				// assertion logic here. We're currently not using it because the message
				// order isn't quite perfect when a remote federated homeserver gets
				// backfilled.
				// validateBatchSendRes(t, remoteCharlie, roomID, batchSendRes, false)
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
				alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
					return ev.Get("event_id").Str == insertionEventID
				}))

				// eventIDsAfter
				createMessagesInRoom(t, alice, roomID, 3)

				batchSendRes := batchSendHistoricalMessages(
					t,
					as,
					roomID,
					eventIdBefore,
					batchID,
					createJoinStateEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore),
					createMessageEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore, 2),
					// Status
					200,
				)
				batchSendResBody := client.ParseJSON(t, batchSendRes)
				historicalEventIDs := client.GetJSONFieldStringArray(t, batchSendResBody, "event_ids")

				// Send the marker event which lets remote homeservers know there are
				// some historical messages back at the given insertion event.
				sendMarkerAndEnsureBackfilled(t, as, alice, roomID, insertionEventID)

				// Join the room from a remote homeserver after the historical messages were sent
				remoteCharlie.JoinRoom(t, roomID, []string{"hs1"})

				// Make sure all of the events have been backfilled
				fetchUntilMessagesResponseHas(t, remoteCharlie, roomID, func(ev gjson.Result) bool {
					if ev.Get("event_id").Str == eventIdBefore {
						return true
					}

					return false
				})

				// FIXME: In the future, we should probably replace the following logic
				// with `validateBatchSendRes` to re-use and have some more robust
				// assertion logic here. We're currently not using it because the message
				// order isn't quite perfect when a remote federated homeserver gets
				// backfilled.
				// validateBatchSendRes(t, remoteCharlie, roomID, batchSendRes, false)
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

				// Join the room from a remote homeserver before any historical messages are sent
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
					createJoinStateEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore),
					createMessageEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore, numMessagesSent),
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
				eventDebugStringsFromBeforeMarkerResponse := mustGetRelevantEventDebugStringsFromMessagesResponse(t, "chunk", beforeMarkerMesssageResBody, relevantToScrollbackEventFilter)
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

				// Send the marker event which lets remote homeservers know there are
				// some historical messages back at the given insertion event.
				sendMarkerAndEnsureBackfilled(t, as, remoteCharlie, roomID, baseInsertionEventID)

				// FIXME: In the future, we should probably replace the following logic
				// with `validateBatchSendRes` to re-use and have some more robust
				// assertion logic here. We're currently not using it because the message
				// order isn't quite perfect when a remote federated homeserver gets
				// backfilled.
				// validateBatchSendRes(t, remoteCharlie, roomID, batchSendRes, false)
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

				// Join the room from a remote homeserver before any historical messages are sent
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
					createJoinStateEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore),
					createMessageEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore, numMessagesSent),
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
				eventDebugStringsFromBeforeMarkerResponse := mustGetRelevantEventDebugStringsFromMessagesResponse(t, "chunk", beforeMarkerMesssageResBody, relevantToScrollbackEventFilter)
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

				// Send the marker event which lets remote homeservers know there are
				// some historical messages back at the given insertion event.
				sendMarkerAndEnsureBackfilled(t, as, remoteCharlie, roomID, baseInsertionEventID)

				// FIXME: In the future, we should probably replace the following logic
				// with `validateBatchSendRes` to re-use and have some more robust
				// assertion logic here. We're currently not using it because the message
				// order isn't quite perfect when a remote federated homeserver gets
				// backfilled.
				// validateBatchSendRes(t, remoteCharlie, roomID, batchSendRes, false)
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

				// Create the "live" event we are going to import our historical events next to
				eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
				eventIdBefore := eventIDsBefore[0]
				timeAfterEventBefore := time.Now()

				// Create eventIDsAfter to avoid the "No forward extremities left!" 500 error from Synapse
				createMessagesInRoom(t, alice, roomID, 2)

				// Import a historical event
				batchSendRes := batchSendHistoricalMessages(
					t,
					as,
					roomID,
					eventIdBefore,
					"",
					createJoinStateEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore),
					createMessageEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore, 1),
					// Status
					200,
				)
				// Make sure we can see all of the historical messages
				validateBatchSendRes(t, as, roomID, batchSendRes, false)
				// Grab the `next_batch_id` for the next batch
				batchSendResBody := client.ParseJSON(t, batchSendRes)
				nextBatchID := client.GetJSONFieldStr(t, batchSendResBody, "next_batch_id")

				// Now try to do a subsequent batch send. This will make sure that
				// insertion events are stored/tracked and can be matched up in the next
				// batch
				batchSendRes1 := batchSendHistoricalMessages(
					t,
					as,
					roomID,
					eventIdBefore,
					nextBatchID,
					createJoinStateEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore),
					createMessageEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore, 1),
					// Status
					200,
				)
				// Make sure we can see all of the historical messages
				validateBatchSendRes(t, as, roomID, batchSendRes1, false)
			})

			t.Run("Not allowed to redact MSC2716 insertion, batch, marker events", func(t *testing.T) {
				t.Parallel()

				roomID := as.CreateRoom(t, createUnsupportedMSC2716RoomOpts)
				alice.JoinRoom(t, roomID, nil)

				// Create the "live" event we are going to import our historical events next to
				eventIDsBefore := createMessagesInRoom(t, alice, roomID, 1)
				eventIdBefore := eventIDsBefore[0]
				timeAfterEventBefore := time.Now()

				// Import a historical event
				batchSendRes := batchSendHistoricalMessages(
					t,
					as,
					roomID,
					eventIdBefore,
					"",
					createJoinStateEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore),
					createMessageEventsForBatchSendRequest([]string{virtualUserID}, timeAfterEventBefore, 1),
					// Status
					200,
				)
				batchSendResBody := client.ParseJSON(t, batchSendRes)
				insertionEventID := client.GetJSONFieldStr(t, batchSendResBody, "insertion_event_id")
				batchEventID := client.GetJSONFieldStr(t, batchSendResBody, "batch_event_id")
				baseInsertionEventID := client.GetJSONFieldStr(t, batchSendResBody, "base_insertion_event_id")

				// Send the marker event which lets remote homeservers know there are
				// some historical messages back at the given insertion event.
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

// Find a given "needle" string in a list of strings, the haystack
func includes(needle string, haystack []string) bool {
	for _, item := range haystack {
		if needle == item {
			return true
		}
	}

	return false
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

func historicalEventFilter(r gjson.Result) bool {
	// This includes messages, insertion, batch, and marker events because we
	// include the historical field on all of them.
	return r.Get("content").Get(strings.ReplaceAll(historicalContentField, ".", "\\.")).Exists()
}

func relevantToScrollbackEventFilter(r gjson.Result) bool {
	return r.Get("type").Str == "m.room.message" || historicalEventFilter(r)
}

func mustGetRelevantEventDebugStringsFromMessagesResponse(t *testing.T, wantKey string, body []byte, eventFilter func(gjson.Result) bool) (eventIDsFromResponse []string) {
	t.Helper()

	debugStrings, err := getRelevantEventDebugStringsFromMessagesResponse(wantKey, body, eventFilter)
	if err != nil {
		t.Fatal(err)
	}

	return debugStrings
}

func getRelevantEventDebugStringsFromMessagesResponse(wantKey string, body []byte, eventFilter func(gjson.Result) bool) (eventIDsFromResponse []string, err error) {
	res := gjson.GetBytes(body, wantKey)
	if !res.Exists() {
		return nil, fmt.Errorf("missing key '%s'", wantKey)
	}
	if !res.IsArray() {
		return nil, fmt.Errorf("key '%s' is not an array (was %s)", wantKey, res.Type)
	}

	res.ForEach(func(key, r gjson.Result) bool {
		if eventFilter(r) {
			eventIDsFromResponse = append(eventIDsFromResponse, r.Get("event_id").Str+" ("+r.Get("content").Get("body").Str+")")
		}
		return true
	})

	return eventIDsFromResponse, nil
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

// Send the marker event which lets remote homeservers know there are
// some historical messages back at the given insertion event.
func sendMarkerAndEnsureBackfilled(t *testing.T, as *client.CSAPI, c *client.CSAPI, roomID, insertionEventID string) (markerEventID string) {
	t.Helper()

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
	c.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
		return ev.Get("event_id").Str == markerEventID
	}))

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

func createInviteStateEventsForBacthSendRequest(
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

func createJoinStateEventsForBatchSendRequest(
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
				"membership":  "join",
				"displayname": fmt.Sprintf("some-display-name-for-%s", virtualUserID),
			},
			"state_key": virtualUserID,
		}

		stateEvents[i] = joinEvent
	}

	return stateEvents
}

func createMessageEventsForBatchSendRequest(
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

// Verify that the batch of historical messages looks correct and in order
// (newest -> oldest) from /messages?dir=b. We can also optionally check that
// the historical state resolves for that chunk of messages.
//
// Note: the historical state will only resolve correctly if the first message
// of `/messages` is one of messages in the historical batch.
func validateBatchSendRes(t *testing.T, c *client.CSAPI, roomID string, batchSendRes *http.Response, validateState bool) {
	t.Helper()

	batchSendResBody0 := client.ParseJSON(t, batchSendRes)
	// Since the original body can only be read once, create a new one from the
	// body bytes we just read
	batchSendRes.Body = ioutil.NopCloser(bytes.NewBuffer(batchSendResBody0))

	historicalEventIDs := client.GetJSONFieldStringArray(t, batchSendResBody0, "event_ids")
	stateEventIDs := client.GetJSONFieldStringArray(t, batchSendResBody0, "state_event_ids")
	batchEventID := client.GetJSONFieldStr(t, batchSendResBody0, "batch_event_id")
	insertionEventID := client.GetJSONFieldStr(t, batchSendResBody0, "insertion_event_id")
	baseInsertionEventID := gjson.GetBytes(batchSendResBody0, "base_insertion_event_id").Str

	// Expected list is ordered from newest -> oldest event time
	var expectedEventIDOrder []string
	if baseInsertionEventID != "" {
		expectedEventIDOrder = append(expectedEventIDOrder, baseInsertionEventID)
	}
	expectedEventIDOrder = append(expectedEventIDOrder, batchEventID)
	expectedEventIDOrder = append(expectedEventIDOrder, reversed(historicalEventIDs)...)
	expectedEventIDOrder = append(expectedEventIDOrder, insertionEventID)

	if validateState {
		// Get a pagination token for the newest-in-time event in the historical batch itself
		contextRes := c.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "context", expectedEventIDOrder[0]}, client.WithContentType("application/json"), client.WithQueries(url.Values{
			"limit": []string{"0"},
		}))
		contextResBody := client.ParseJSON(t, contextRes)
		batchStartPaginationToken := client.GetJSONFieldStr(t, contextResBody, "end")

		// Fetch a chunk of `/messages` which only contains the historical batch. We
		// want to do this because `/messages` only returns the state for the first
		// message in the `chunk` and we want to be able assert that the historical
		// state is able to be resolved.
		messagesRes := c.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
			// Go backwards (newest -> oldest) (same direction as if you were using scrollback)
			"dir": []string{"b"},
			// From the newest-in-time event in the historical batch
			"from": []string{batchStartPaginationToken},
			// We are aiming to scrollback to the oldest-in-time event from the
			// historical batch
			"limit": []string{fmt.Sprintf("%d", len(expectedEventIDOrder))},
			// We add these options to the filter so we get member events in the state field
			"filter": []string{"{\"lazy_load_members\":true,\"include_redundant_members\":true}"},
		}))

		must.MatchResponse(t, messagesRes, match.HTTPResponse{
			JSON: []match.JSON{
				// Double-check that we're in the right place of scrollback
				matcherJSONEventIDArrayInOrder("chunk",
					expectedEventIDOrder,
					historicalEventFilter,
				),
				// Make sure the historical m.room.member join state event resolves
				// for the given chunk of messages in scrollback. The member event
				// will include the displayname and avatar.
				match.JSONCheckOffAllowUnwanted("state", makeInterfaceSlice(stateEventIDs), func(r gjson.Result) interface{} {
					return r.Get("event_id").Str
				}, nil),
			},
		})
	}

	// Make sure the historical events appear in scrollback without jumping back
	// in time specifically.
	fullMessagesRes := c.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
		"dir":   []string{"b"},
		"limit": []string{"100"},
	}))
	must.MatchResponse(t, fullMessagesRes, match.HTTPResponse{
		JSON: []match.JSON{
			matcherJSONEventIDArrayInOrder("chunk",
				expectedEventIDOrder,
				historicalEventFilter,
			),
		},
	})
}

// matcherJSONEventIDArrayInOrder loops through `jsonArrayKey` in the response
// to find the sliding window of expected event ID's(`expectedEventIDOrder`)
// somewhere in the array in order. The expected list can start anywhere in the
// overall list filtered by `eventFilter`.
func matcherJSONEventIDArrayInOrder(jsonArrayKey string, expectedEventIDOrder []string, eventFilter func(gjson.Result) bool) match.JSON {
	return func(body []byte) error {
		if len(expectedEventIDOrder) == 0 {
			return fmt.Errorf("expectedEventIDOrder can not be an empty list")
		}

		// Copy the array by slice so we can modify it as we iterate in the foreach loop.
		// We save the full untouched `expectedEventIDOrder` for use in the log messages
		workingExpectedEventIDOrder := expectedEventIDOrder

		var res gjson.Result
		if jsonArrayKey == "" {
			res = gjson.ParseBytes(body)
		} else {
			res = gjson.GetBytes(body, jsonArrayKey)
		}

		if !res.Exists() {
			return fmt.Errorf("missing key '%s'", jsonArrayKey)
		}
		if !res.IsArray() {
			return fmt.Errorf("key '%s' is not an array", jsonArrayKey)
		}

		eventDebugStringsFromResponse, err := getRelevantEventDebugStringsFromMessagesResponse("chunk", body, eventFilter)
		if err != nil {
			return err
		}

		// Loop through the overall event list
		foundFirstEvent := false
		res.ForEach(func(_, r gjson.Result) bool {
			eventID := r.Get("event_id").Str
			nextEventIdInOrder := workingExpectedEventIDOrder[0]

			// We need to find the start of the sliding window inside the overall
			// event list
			if !foundFirstEvent && eventID == nextEventIdInOrder {
				foundFirstEvent = true
			}

			// Once we found the first event, find all events in order
			if foundFirstEvent && eventFilter(r) {
				if r.Get("event_id").Str != nextEventIdInOrder {
					err = fmt.Errorf("Next event found was %s but expected %s\nActualEvents (%d): %v\nExpectedEvents (%d): %v", r.Get("event_id").Str, nextEventIdInOrder, len(eventDebugStringsFromResponse), eventDebugStringsFromResponse, len(expectedEventIDOrder), expectedEventIDOrder)
					return false
				}

				// Now that we found it, pop the message off the expected list
				workingExpectedEventIDOrder = workingExpectedEventIDOrder[1:]
			}

			// Found all of the expected events, stop iterating
			if len(workingExpectedEventIDOrder) == 0 {
				return false
			}

			return true
		})

		// There was some left-over events in the list but we should have found all
		// of them
		if len(workingExpectedEventIDOrder) != 0 {
			return fmt.Errorf("Expected all events to be matched in message response but there were some left-over events (%d): %s\nActualEvents (%d): %v\nExpectedEvents (%d): %v", len(workingExpectedEventIDOrder), workingExpectedEventIDOrder, len(eventDebugStringsFromResponse), eventDebugStringsFromResponse, len(expectedEventIDOrder), expectedEventIDOrder)
		}

		return err
	}
}
