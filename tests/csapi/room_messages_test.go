package csapi_tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
	"github.com/matrix-org/gomatrixserverlib/spec"
)

// sytest: POST /rooms/:room_id/send/:event_type sends a message
// sytest: GET /rooms/:room_id/messages returns a message
func TestSendAndFetchMessage(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // flakey
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})

	const testMessage = "TestSendAndFetchMessage"

	_, token := alice.MustSync(t, client.SyncReq{})

	// first use the non-txn endpoint
	alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message"}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    testMessage,
	}))

	// sync until the server has processed it
	alice.MustSyncUntil(t, client.SyncReq{Since: token}, client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		return r.Get("type").Str == "m.room.message" && r.Get("content").Get("body").Str == testMessage
	}))

	// then request messages from the room
	queryParams := url.Values{}
	queryParams.Set("dir", "f")
	queryParams.Set("from", token)
	res := alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONArrayEach("chunk", func(r gjson.Result) error {
				if r.Get("type").Str == "m.room.message" && r.Get("content").Get("body").Str == testMessage {
					return nil
				}
				return fmt.Errorf("did not find correct event")
			}),
		},
	})
}

// With a non-existent room_id, GET /rooms/:room_id/messages returns 403
// forbidden ("You aren't a member of the room").
func TestFetchMessagesFromNonExistentRoom(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	roomID := "!does-not-exist:hs1"

	// then request messages from the room
	queryParams := url.Values{}
	queryParams.Set("dir", "b")
	res := alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusForbidden,
	})
}

// sytest: PUT /rooms/:room_id/send/:event_type/:txn_id sends a message
// sytest: PUT /rooms/:room_id/send/:event_type/:txn_id deduplicates the same txn id
func TestSendMessageWithTxn(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{})

	const txnID = "lorem"

	res := alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", txnID}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "test",
	}))
	eventID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	res = alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", txnID}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "test",
	}))
	eventID2 := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	if eventID != eventID2 {
		t.Fatalf("two /send requests with same txn ID resulted in different event IDs")
	}
}

func TestRoomMessagesLazyLoading(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	charlie := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.MustJoinRoom(t, roomID, nil)
	charlie.MustJoinRoom(t, roomID, nil)

	bob.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "test",
		},
	})

	beforeToken := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID), client.SyncJoinedTo(charlie.UserID, roomID))

	eventID := charlie.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "test",
		},
	})

	afterToken := alice.MustSyncUntil(t, client.SyncReq{Since: beforeToken}, client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		return r.Get("event_id").Str == eventID
	}))

	queryParams := url.Values{}
	queryParams.Set("dir", "f")
	queryParams.Set("filter", `{ "lazy_load_members" : true }`)
	queryParams.Set("from", beforeToken)
	queryParams.Set("to", afterToken)
	res := alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONKeyArrayOfSize("state", 1),
			match.JSONArrayEach("state", func(j gjson.Result) error {
				if j.Get("type").Str != "m.room.member" {
					return fmt.Errorf("state event is not m.room.member")
				}
				if j.Get("state_key").Str != charlie.UserID {
					return fmt.Errorf("m.room.member event is not for charlie")
				}
				if j.Get("content").Get("membership").Str != "join" {
					return fmt.Errorf("m.room.member event is not 'join'")
				}
				return nil
			}),
		},
	})

}

// TODO We should probably see if this should be removed.
//
//	Sytest tests for a very specific bug; check if local user member event loads properly when going backwards from a prev_event.
//	However, note that the sytest *only* checks for the *local, single* user in a room to be included in `/messages`.
//	This function exists here for sytest exhaustiveness, but I question its usefulness, thats why TestRoomMessagesLazyLoading
//	exists to do a more generic check.
//	We should probably see if this should be removed.
//
// sytest: GET /rooms/:room_id/messages lazy loads members correctly
func TestRoomMessagesLazyLoadingLocalUser(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{})

	token := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

	alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "test",
		},
	})

	queryParams := url.Values{}
	queryParams.Set("dir", "b")
	queryParams.Set("filter", `{ "lazy_load_members" : true }`)
	queryParams.Set("from", token)
	res := alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONKeyArrayOfSize("state", 1),
			match.JSONArrayEach("state", func(j gjson.Result) error {
				if j.Get("type").Str != "m.room.member" {
					return fmt.Errorf("state event is not m.room.member")
				}
				if j.Get("state_key").Str != alice.UserID {
					return fmt.Errorf("m.room.member event is not for alice")
				}
				if j.Get("content").Get("membership").Str != "join" {
					return fmt.Errorf("m.room.member event is not 'join'")
				}
				return nil
			}),
		},
	})
}

type MessageDraft struct {
	Sender  *client.CSAPI
	Message string
}

type EventInfo struct {
	MessageDraft MessageDraft
	EventID      string
}

type MessagesTestCase struct {
	name                   string
	numberOfMessagesToSend int
	messagesRequestLimit   int
}

func TestMessagesOverFederation(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
	})
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{
		LocalpartSuffix: "bob",
	})

	// Test to make sure all of the messages sent in the room are visible to someone else
	// who joins the room later on.
	t.Run("Visible shared history after joining new room (backfill)", func(t *testing.T) {
		// FIXME: Dendrite doesn't handle backfill here for whatever reason
		runtime.SkipIf(t, runtime.Dendrite)

		// Some homeservers have different hard-limits for `/messages?limit=xxx` requests
		// (Synapse's `MAX_LIMIT` is 1000) so we test a few different variations.
		for _, testCase := range []MessagesTestCase{
			// Test where the `/messages?limit=xxx` is <= than the number of messages the
			// homeserver tries to backfill before responding to the `/messages` request.
			// Because the Matrix spec default `limit` is 10, we can assume that this is lower
			// than the number of messages that *any* homeserver will try to backfill before
			// responding.
			{
				name: "`messagesRequestLimit` is lower than the number of messages backfilled (assumed)",
				// We send more messages than fit in one request
				numberOfMessagesToSend: 20,
				// This is the default limit in the Matrix spec so it's bound to be lower than
				// the number of messages that are backfilled.
				messagesRequestLimit: 10,
			},
			// Test where the `/messages?limit=xxx` is greater than the number of messages
			// Synapse tries to backfill (100) before responding to the `/messages` request.
			{
				name: "`messagesRequestLimit` is greater than the number of messages backfilled (in Synapse, 100)",
				// We send more messages than fit in one request
				numberOfMessagesToSend: 300,
				// We request more messages than Synapse tries to backfill at once (which is 100)
				messagesRequestLimit: 200,
			},
		} {
			t.Run(testCase.name, func(t *testing.T) {
				// Alice creates the room
				roomID := alice.MustCreateRoom(t, map[string]interface{}{
					// The `public_chat` preset includes `history_visibility: "shared"` ("Previous
					// events are always accessible to newly joined members. All events in the
					// room are accessible, even those sent when the member was not a part of the
					// room."), which is what we want to test.
					"preset": "public_chat",
				})

				// Send messages and make sure we can see them in `/messages`
				_sendAndTestMessageHistory(
					t,
					roomID,
					deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
					alice,
					bob,
					testCase,
				)
			})
		}
	})

	// Test to make sure all of the messages sent in the room are visible to someone else
	// who *re-joins* the room.
	t.Run("Visible shared history after re-joining room (backfill)", func(t *testing.T) {
		// FIXME: Dendrite doesn't handle backfill well on re-join yet
		runtime.SkipIf(t, runtime.Dendrite)

		// Some homeservers have different hard-limits for `/messages?limit=xxx` requests
		// (Synapse's `MAX_LIMIT` is 1000) so we test a few different variations.
		for _, testCase := range []MessagesTestCase{
			// Test where the `/messages?limit=xxx` is <= than the number of messages the
			// homeserver tries to backfill before responding to the `/messages` request.
			// Because the Matrix spec default `limit` is 10, we can assume that this is lower
			// than the number of messages that *any* homeserver will try to backfill before
			// responding.
			{
				name: "`messagesRequestLimit` is lower than the number of messages backfilled (assumed)",
				// We send more messages than fit in one request
				numberOfMessagesToSend: 20,
				// This is the default limit in the Matrix spec so it's bound to be lower than
				// the number of messages that are backfilled.
				messagesRequestLimit: 10,
			},
			// Test where the `/messages?limit=xxx` is greater than the number of messages
			// Synapse tries to backfill (100) before responding to the `/messages` request.
			//
			// FIXME: This test currently doesn't work because the homeserver will backfill
			// the `limit=100` and return those 100 new events + all of the old history
			// leaving an invisible gap in between. So the events in the response includes the
			// 100 new events, [gap], the old history from when you were previously joined.
			// This is the type of scenario that MSC3871 (Gappy timelines) is trying to
			// address. This is a hole in the spec as there is no way for a homeserver
			// indicate gaps to the client so they can paginate the gap and cause the
			// homeserver to backfill more.
			//
			// {
			// 	name: "`messagesRequestLimit` is greater than the number of messages backfilled (in Synapse, 100)",
			// 	// We send more messages than fit in one request
			// 	numberOfMessagesToSend: 300,
			// 	// We request more messages than Synapse tries to backfill at once (which is 100)
			// 	messagesRequestLimit: 200,
			// },
		} {
			t.Run(testCase.name, func(t *testing.T) {
				// Start a sync loop
				_, aliceSince := alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

				// Alice creates the room
				roomID := alice.MustCreateRoom(t, map[string]interface{}{
					// The `public_chat` preset includes `history_visibility: "shared"` ("Previous
					// events are always accessible to newly joined members. All events in the
					// room are accessible, even those sent when the member was not a part of the
					// room."), which is what we want to test.
					"preset": "public_chat",
				})

				// Bob joins the room
				bob.MustJoinRoom(t, roomID, []spec.ServerName{
					deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
				})
				aliceSince = alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncJoinedTo(bob.UserID, roomID))

				// Bob leaves the room
				bob.MustLeaveRoom(t, roomID)
				// Make sure the leave has federated
				aliceSince = alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncLeftFrom(bob.UserID, roomID))

				// Send messages and make sure we can see them in `/messages`
				_sendAndTestMessageHistory(
					t,
					roomID,
					deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
					alice,
					bob,
					testCase,
				)
			})
		}
	})
}

// 1. Alice sends a bunch of messages into the room
// 2. Bob joins the room
// 3. Bob paginates backwards through the room history until he reaches the start of the room
// 4. Assert that Bob sees all of the messages that Alice sent in the correct order
func _sendAndTestMessageHistory(
	t *testing.T,
	roomID string,
	serverToJoinVia spec.ServerName,
	alice, bob *client.CSAPI,
	testCase MessagesTestCase,
) {
	// Keep track of the order
	eventIDs := make([]string, 0)
	// Map from event_id to event info
	eventMap := make(map[string]EventInfo)

	messageDrafts := make([]MessageDraft, 0, testCase.numberOfMessagesToSend)
	for i := 0; i < testCase.numberOfMessagesToSend; i++ {
		messageDrafts = append(messageDrafts, MessageDraft{alice, fmt.Sprintf("Filler message %d to increase history size.", i+1)})
	}
	sendAndTrackMessages(t, roomID, messageDrafts, &eventIDs, &eventMap)

	// Bob joins the room
	bob.MustJoinRoom(t, roomID, []spec.ServerName{
		serverToJoinVia,
	})

	// Make it easy to cross-reference the events being talked about in the logs
	for eventIndex, eventID := range eventIDs {
		t.Logf("Message %d -> event_id=%s", eventIndex, eventID)
	}

	// Keep paginating backwards until we reach the start of the room
	reverseChronologicalActualEventIDs := make(
		[]string,
		0,
		// This is a minimum capacity (there will be more events)
		testCase.numberOfMessagesToSend,
	)
	fromToken := ""
	for {
		messageQueryParams := url.Values{
			"dir":   []string{"b"},
			"limit": []string{strconv.Itoa(testCase.messagesRequestLimit)},
		}
		if fromToken != "" {
			messageQueryParams.Set("from", fromToken)
		}

		messagesRes := bob.MustDo(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"},
			client.WithContentType("application/json"),
			client.WithQueries(messageQueryParams),
		)
		messagesResBody := client.ParseJSON(t, messagesRes)
		actualEventIDsFromRequest := extractEventIDsFromMessagesResponse(t, messagesResBody)
		reverseChronologicalActualEventIDs = append(reverseChronologicalActualEventIDs, actualEventIDsFromRequest...)

		// Make it easy to understand what each `/messages` request returned
		relevantActualEventIDsFromRequest := filterEventIDs(t, actualEventIDsFromRequest, eventIDs)
		firstEventIndex := -1
		lastEventIndex := -1
		if len(relevantActualEventIDsFromRequest) > 0 {
			firstEventIndex = slices.Index(eventIDs, relevantActualEventIDsFromRequest[0])
			lastEventIndex = slices.Index(eventIDs, relevantActualEventIDsFromRequest[len(relevantActualEventIDsFromRequest)-1])
		}
		t.Logf("Fetched %d events from the `/messages` endpoint that included events %d to %d",
			len(actualEventIDsFromRequest),
			firstEventIndex, lastEventIndex,
		)

		endTokenRes := gjson.GetBytes(messagesResBody, "end")
		// "`end`: If no further events are available (either because we have reached the
		// start of the timeline, or because the user does not have permission to see
		// any more events), this property is omitted from the response." (Matrix spec)
		if !endTokenRes.Exists() {
			break
		}
		fromToken = endTokenRes.Str

		// Or if we don't see any more events, we will assume that we reached the
		// start of the room. No more to paginate.
		if len(actualEventIDsFromRequest) == 0 {
			break
		}
	}

	// Put them in chronological order to match the expected list
	chronologicalActualEventIds := slices.Clone(reverseChronologicalActualEventIDs)
	slices.Reverse(chronologicalActualEventIds)

	// Assert timeline order
	assertMessagesInTimelineInOrder(t, chronologicalActualEventIds, eventIDs)
}

func sendMessageDrafts(
	t *testing.T,
	roomID string,
	messageDrafts []MessageDraft,
) []string {
	t.Helper()

	eventIDs := make([]string, len(messageDrafts))
	for messageDraftIndex, messageDraft := range messageDrafts {
		eventID := messageDraft.Sender.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    messageDraft.Message,
			},
		})
		eventIDs[messageDraftIndex] = eventID
	}

	return eventIDs
}

// sendAndTrackMessages sends the given message drafts to the room, keeping track of the
// new events in the list of `eventIDs` and `eventMap`. Returns the list of new event
// IDs that were sent.
func sendAndTrackMessages(
	t *testing.T,
	roomID string,
	messageDrafts []MessageDraft,
	eventIDs *[]string,
	eventMap *map[string]EventInfo,
) []string {
	t.Helper()

	newEventIDs := sendMessageDrafts(t, roomID, messageDrafts)

	*eventIDs = append(*eventIDs, newEventIDs...)
	for i, eventID := range newEventIDs {
		(*eventMap)[eventID] = EventInfo{
			MessageDraft: messageDrafts[i],
			EventID:      eventID,
		}
	}

	return newEventIDs
}

// extractEventIDsFromMessagesResponse extracts the event IDs from the given
// `/messages` response body.
func extractEventIDsFromMessagesResponse(
	t *testing.T,
	messagesResBody json.RawMessage,
) []string {
	t.Helper()

	wantKey := "chunk"
	keyRes := gjson.GetBytes(messagesResBody, wantKey)
	if !keyRes.Exists() {
		t.Fatalf("extractEventIDsFromMessagesResponse: missing key '%s'", wantKey)
	}
	if !keyRes.IsArray() {
		t.Fatalf("extractEventIDsFromMessagesResponse: key '%s' is not an array (was %s)", wantKey, keyRes.Type)
	}

	var eventIDs []string
	actualEvents := keyRes.Array()
	for _, event := range actualEvents {
		eventIDs = append(eventIDs, event.Get("event_id").Str)
	}

	return eventIDs
}

func filterEventIDs(t *testing.T, actualEventIDs []string, expectedEventIDs []string) []string {
	t.Helper()

	relevantActualEventIDs := make([]string, 0, len(expectedEventIDs))
	for _, eventID := range actualEventIDs {
		if slices.Contains(expectedEventIDs, eventID) {
			relevantActualEventIDs = append(relevantActualEventIDs, eventID)
		}
	}

	return relevantActualEventIDs
}

// assertMessagesTimeline asserts all events are in the `/messages` response in the
// given order. Other unrelated events can be in between.
func assertMessagesInTimelineInOrder(t *testing.T, actualEventIDs []string, expectedEventIDs []string) {
	t.Helper()

	relevantActualEventIDs := filterEventIDs(t, actualEventIDs, expectedEventIDs)

	expectedLines := make([]string, len(expectedEventIDs))
	for i, expectedEventID := range expectedEventIDs {
		isExpectedInActual := slices.Contains(relevantActualEventIDs, expectedEventID)
		isMissingIndicatorString := " "
		if !isExpectedInActual {
			isMissingIndicatorString = "?"
		}

		expectedLines[i] = fmt.Sprintf("%2d: %s  %s", i, isMissingIndicatorString, expectedEventID)
	}
	expectedDiffString := strings.Join(expectedLines, "\n")

	actualLines := make([]string, len(relevantActualEventIDs))
	for actualEventIndex, actualEventID := range relevantActualEventIDs {
		isActualInExpected := slices.Contains(expectedEventIDs, actualEventID)
		isActualInExpectedIndicatorString := " "
		if isActualInExpected {
			isActualInExpectedIndicatorString = "+"
		}

		expectedIndex := slices.Index(expectedEventIDs, actualEventID)
		expectedIndexString := ""
		if actualEventIndex != expectedIndex {
			expectedDirectionString := "⬆️"
			if expectedIndex > actualEventIndex {
				expectedDirectionString = "⬇️"
			}

			expectedIndexString = fmt.Sprintf(" (expected index %d %s)", expectedIndex, expectedDirectionString)
		}

		actualLines[actualEventIndex] = fmt.Sprintf("%2d: %s  %s%s", actualEventIndex, isActualInExpectedIndicatorString, actualEventID, expectedIndexString)
	}
	actualDiffString := strings.Join(actualLines, "\n")

	if len(relevantActualEventIDs) != len(expectedEventIDs) {
		t.Fatalf("expected %d events in timeline (got %d)\nActual events ('+' = found expected items):\n%s\nExpected events ('?' = missing expected items):\n%s",
			len(expectedEventIDs), len(relevantActualEventIDs), actualDiffString, expectedDiffString,
		)
	}

	for i, eventID := range relevantActualEventIDs {
		if eventID != expectedEventIDs[i] {
			t.Fatalf("expected event ID %s (got %s) at index %d\nActual events ('+' = found expected items):\n%s\nExpected events ('?' = missing expected items):\n%s",
				expectedEventIDs[i], eventID, i, actualDiffString, expectedDiffString,
			)
		}
	}
}
