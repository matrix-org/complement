package csapi_tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
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

func TestRoomMessagesGaps(t *testing.T) {
	deployment := complement.Deploy(t, 3)
	defer deployment.Destroy(t)

	// Sometimes we send more than 10 messages (the default in Synapse) and we want to
	// include all of them in the response.
	includeMoreTimelineFilter, _ := json.Marshal(map[string]interface{}{
		"room": map[string]interface{}{
			"timeline": map[string]interface{}{
				"limit": 100,
			},
		},
	})

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
	})
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{
		LocalpartSuffix: "bob",
	})
	charlie := deployment.Register(t, "hs3", helpers.RegistrationOpts{
		LocalpartSuffix: "charlie",
	})

	// Start a sync loop
	_, aliceSince := alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})
	_, bobSince := bob.MustSync(t, client.SyncReq{TimeoutMillis: "0"})
	_, charlieSince := charlie.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

	// Keep track of the order
	eventIDs := make([]string, 0)
	// Map from event_id to event info
	eventMap := make(map[string]EventInfo)
	// List of join events from charlie
	charlieJoinEventIDs := make([]string, 0)

	// Everyone joins the room
	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.MustJoinRoom(t, roomID, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
	})
	awaitPartialStateJoinCompletion(t, roomID, bob)
	charlie.MustJoinRoom(t, roomID, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
	})
	charlieJoinEventID := getStateID(t, charlie, roomID, "m.room.member", charlie.UserID)
	charlieJoinEventIDs = append(charlieJoinEventIDs, charlieJoinEventID)
	t.Logf("Charlie initially joins the room: %s", charlieJoinEventID)
	awaitPartialStateJoinCompletion(t, roomID, charlie)

	messageDrafts := []MessageDraft{
		MessageDraft{alice, "I was just reading that commercial moon trips might start next year."},
		MessageDraft{bob, "Seriously? I'd sign up in a heartbeat. Imagine looking back at Earth."},
		MessageDraft{charlie, "Yeah, me too. It's the ultimate adventure. I've actually been looking into it..."},
		MessageDraft{alice, "Wait, Charlie, you're not actually considering it, are you? It must be incredibly dangerous."},
		MessageDraft{charlie, "Considering it? My launch is in ten minutes. Gotta go suit up."},
		MessageDraft{bob, "Wait, what? You're joking. Right, Charlie?"},
	}
	newEventIDs := sendAndTrackMessages(t, roomID, messageDrafts, &eventIDs, &eventMap)
	// Make sure all of the messages have federated
	aliceSince = alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince, Filter: string(includeMoreTimelineFilter)}, syncTimelineHasEventIDs(roomID, newEventIDs)...)
	bobSince = bob.MustSyncUntil(t, client.SyncReq{Since: bobSince, Filter: string(includeMoreTimelineFilter)}, syncTimelineHasEventIDs(roomID, newEventIDs)...)
	charlieSince = charlie.MustSyncUntil(t, client.SyncReq{Since: charlieSince, Filter: string(includeMoreTimelineFilter)}, syncTimelineHasEventIDs(roomID, newEventIDs)...)

	// Charlie leaves the room
	charlie.MustLeaveRoom(t, roomID)
	t.Logf("Charlie leaving for the moon: %s", getStateID(t, charlie, roomID, "m.room.member", charlie.UserID))
	// Make sure the leave has federated
	aliceSince = alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncLeftFrom(charlie.UserID, roomID))
	bobSince = bob.MustSyncUntil(t, client.SyncReq{Since: bobSince}, client.SyncLeftFrom(charlie.UserID, roomID))
	charlieSince = charlie.MustSyncUntil(t, client.SyncReq{Since: charlieSince}, client.SyncLeftFrom(charlie.UserID, roomID))

	// Send some more messages which charlie won't get
	messageDrafts = []MessageDraft{
		MessageDraft{alice, "Charlie...?"},
		MessageDraft{bob, "I think he was serious. His profile pic is now him in a spacesuit."},
		MessageDraft{alice, "Well. I guess he really left for the moon. Talk about a conversation killer."},
	}
	newEventIDs = sendAndTrackMessages(t, roomID, messageDrafts, &eventIDs, &eventMap)
	// Make sure all of the messages have federated
	aliceSince = alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince, Filter: string(includeMoreTimelineFilter)}, syncTimelineHasEventIDs(roomID, newEventIDs)...)
	bobSince = bob.MustSyncUntil(t, client.SyncReq{Since: bobSince, Filter: string(includeMoreTimelineFilter)}, syncTimelineHasEventIDs(roomID, newEventIDs)...)
	// Charlie isn't in the room right now so won't see anything yet
	// charlieSince = charlie.MustSyncUntil(t, client.SyncReq{Since: charlieSince, Filter: string(includeMoreTimelineFilter)}, syncTimelineHasEventIDs(roomID, newEventIDs)...)

	// Charlie joins back after going to the moon (has a gap in history)
	charlie.MustJoinRoom(t, roomID, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
	})
	charlieJoinEventID = getStateID(t, charlie, roomID, "m.room.member", charlie.UserID)
	charlieJoinEventIDs = append(charlieJoinEventIDs, charlieJoinEventID)
	t.Logf("Charlie join after coming back from the moon: %s", charlieJoinEventID)
	awaitPartialStateJoinCompletion(t, roomID, charlie)
	// Make sure the join has federated
	aliceSince = alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncJoinedTo(charlie.UserID, roomID))
	bobSince = bob.MustSyncUntil(t, client.SyncReq{Since: bobSince}, client.SyncJoinedTo(charlie.UserID, roomID))
	charlieSince = charlie.MustSyncUntil(t, client.SyncReq{Since: charlieSince}, client.SyncJoinedTo(charlie.UserID, roomID))

	messageDrafts = []MessageDraft{
		MessageDraft{bob, "Hey, has anyone heard from Charlie? It's been months."},
		MessageDraft{alice, "Not a peep. I still can't believe he actually did it."},
		MessageDraft{charlie, "Believe it."},
		MessageDraft{alice, "CHARLIE?! You're back! How was it?!"},
		MessageDraft{charlie, "Dusty. Quiet. The most beautiful thing I've ever seen. Earth is just... a blue marble."},
		MessageDraft{bob, "Welcome back, man! So, what's next? A well-deserved vacation on a beach?"},
		MessageDraft{charlie, "A beach? Nah. I've seen the next horizon."},
		MessageDraft{alice, "Oh no. I know that tone. What horizon?"},
		MessageDraft{charlie, "The red one. They need pilots for the new Mars colony. I leave in six weeks."},
		MessageDraft{bob, "You can't be serious. You just got back!"},
		MessageDraft{charlie, "Serious as a vacuum. Talk to you guys from the stars. Bob, Alice... try to keep Earth in one piece for me."},
	}
	newEventIDs = sendAndTrackMessages(t, roomID, messageDrafts, &eventIDs, &eventMap)
	// Make sure all of the messages have federated
	aliceSince = alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince, Filter: string(includeMoreTimelineFilter)}, syncTimelineHasEventIDs(roomID, newEventIDs)...)
	bobSince = bob.MustSyncUntil(t, client.SyncReq{Since: bobSince, Filter: string(includeMoreTimelineFilter)}, syncTimelineHasEventIDs(roomID, newEventIDs)...)
	charlieSince = charlie.MustSyncUntil(t, client.SyncReq{Since: charlieSince, Filter: string(includeMoreTimelineFilter)}, syncTimelineHasEventIDs(roomID, newEventIDs)...)

	// Charlie leaves the room
	charlie.MustLeaveRoom(t, roomID)
	t.Logf("Charlie leaving to Mars: %s", getStateID(t, charlie, roomID, "m.room.member", charlie.UserID))
	// Make sure the leave has federated
	aliceSince = alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncLeftFrom(charlie.UserID, roomID))
	bobSince = bob.MustSyncUntil(t, client.SyncReq{Since: bobSince}, client.SyncLeftFrom(charlie.UserID, roomID))
	charlieSince = charlie.MustSyncUntil(t, client.SyncReq{Since: charlieSince}, client.SyncLeftFrom(charlie.UserID, roomID))

	// Send some more messages while charlie is gone
	messageDrafts = []MessageDraft{
		MessageDraft{bob, "Okay, so with Charlie literally out of this world, who's watering his plants?"},
		MessageDraft{alice, "I have a key. I'm on it. Though I'm half-convinced his fern is planning a moon landing of its own."},
		MessageDraft{bob, "Hah! So, completely changing the subject, have you tried that new pizza place on 5th? The one with the weird hexagonal slices?"},
		MessageDraft{alice, "Hexagonza? Yeah! The 'Geometry Special' is actually amazing. Though eating it feels like a math test."},
		MessageDraft{bob, "Right? I kept trying to calculate the area. Totally worth the existential crisis though."},
		MessageDraft{alice, "We should go next week. My treat. We can finally have a conversation that doesn't involve orbital mechanics."},
		MessageDraft{bob, "Deal. But low-key, I'm still expecting Charlie to message us a photo of his pizza on Mars."},
		MessageDraft{alice, "With extra red dust."},
	}
	newEventIDs = sendAndTrackMessages(t, roomID, messageDrafts, &eventIDs, &eventMap)
	// Make sure all of the messages have federated
	aliceSince = alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince, Filter: string(includeMoreTimelineFilter)}, syncTimelineHasEventIDs(roomID, newEventIDs)...)
	bobSince = bob.MustSyncUntil(t, client.SyncReq{Since: bobSince, Filter: string(includeMoreTimelineFilter)}, syncTimelineHasEventIDs(roomID, newEventIDs)...)
	// Charlie isn't in the room right now so won't see anything yet
	// charlieSince = charlie.MustSyncUntil(t, client.SyncReq{Since: charlieSince, Filter: string(includeMoreTimelineFilter)}, syncTimelineHasEventIDs(roomID, newEventIDs)...)

	// Charlie joins back after going to mars (has a gap in history)
	charlie.MustJoinRoom(t, roomID, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
	})
	charlieJoinEventID = getStateID(t, charlie, roomID, "m.room.member", charlie.UserID)
	charlieJoinEventIDs = append(charlieJoinEventIDs, charlieJoinEventID)
	t.Logf("Charlie join after coming back from Mars: %s", charlieJoinEventID)
	awaitPartialStateJoinCompletion(t, roomID, charlie)
	// Make sure the join has federated
	aliceSince = alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncJoinedTo(charlie.UserID, roomID))
	bobSince = bob.MustSyncUntil(t, client.SyncReq{Since: bobSince}, client.SyncJoinedTo(charlie.UserID, roomID))
	charlieSince = charlie.MustSyncUntil(t, client.SyncReq{Since: charlieSince}, client.SyncJoinedTo(charlie.UserID, roomID))

	// Make it easy to cross-reference the events being talked about in the logs
	for eventIndex, eventID := range eventIDs {
		// messageDraft := eventMap[eventID].MessageDraft
		t.Logf("Message %d -> event_id=%s", eventIndex, eventID)
	}

	messagesRes := charlie.MustDo(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"},
		client.WithContentType("application/json"),
		client.WithQueries(url.Values{
			"dir":      []string{"b"},
			"limit":    []string{"100"},
			"backfill": []string{"false"},
		}),
	)
	messagesResBody := client.ParseJSON(t, messagesRes)
	t.Logf("Before backfill (expecting gaps) %s", messagesResBody)

	// We should see some gaps
	gapsRes := gjson.GetBytes(messagesResBody, "gaps")
	if !gapsRes.Exists() {
		t.Fatalf("missing key '%s' in JSON response", "gaps")
	}
	if !gapsRes.IsArray() {
		t.Fatalf("key '%s' is not an array (was %s)", "gaps", gapsRes.Type)
	}
	gaps := gapsRes.Array()
	if len(gaps) != 3 {
		t.Fatalf("expected 3 gaps (got %d) for each time after charlie joins back to the room - gaps: %s",
			len(gaps), gaps,
		)
	}
	// Assert gaps are where we expect
	for gapIndex, gap := range gaps {
		if gaps[gapIndex].Get("event_id").Str != charlieJoinEventIDs[len(charlieJoinEventIDs)-1-gapIndex] {
			t.Fatalf("expected gap %d event_id to be %s (got %s) - charlieJoinEventIDs: %s",
				gapIndex,
				charlieJoinEventIDs[len(charlieJoinEventIDs)-1-gapIndex],
				gap.Get("event_id").Str,
				charlieJoinEventIDs,
			)
		}
	}

	// Fetch with `?backfill=true` to close the gaps
	for _, gap := range gaps {
		// TODO: Do a better job of retrying until we see the new event. Not every server
		// implementation will necessarily backfill right away in the foreground of a
		// `/messages` request.
		charlie.MustDo(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"},
			client.WithContentType("application/json"),
			client.WithQueries(url.Values{
				"dir":      []string{"b"},
				"limit":    []string{"100"},
				"backfill": []string{"true"},
				"from":     []string{gap.Get("prev_pagination_token").Str},
			}),
		)
	}

	// Make another `/messages` request to ensure that we've backfilled the events now and
	// we don't see any gaps
	messagesRes = charlie.MustDo(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"},
		client.WithContentType("application/json"),
		client.WithQueries(url.Values{
			"dir":      []string{"b"},
			"limit":    []string{"100"},
			"backfill": []string{"false"},
		}),
	)
	messagesResBody = client.ParseJSON(t, messagesRes)
	t.Logf("After backfill (expecting *no* gaps) %s", messagesResBody)

	// We shouldn't see any gaps anymore
	gapsRes = gjson.GetBytes(messagesResBody, "gaps")
	// The gaps array could be empty (or omitted entirely)
	if gapsRes.Exists() {
		gaps = gapsRes.Array()
		if len(gaps) != 0 {
			t.Logf("Gaps after backfill (unexpected): %s", gaps)
			// t.Fatalf("expected no gaps (got %d) after we backfilled each one - gaps: %s",
			// 	len(gaps), gaps,
			// )
		}
	} else {
		// Omitted entirely is fine (no gaps)
	}

	// Assert timeline order
	assertMessagesInTimelineInOrder(t, messagesResBody, eventIDs)
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

func syncTimelineHasEventIDs(roomID string, eventIDs []string) []client.SyncCheckOpt {
	syncChecks := make([]client.SyncCheckOpt, 0, len(eventIDs))
	for _, eventID := range eventIDs {
		syncChecks = append(syncChecks, client.SyncTimelineHasEventID(roomID, eventID))
	}
	return syncChecks
}

// assertMessagesTimeline asserts all events are in the response in the given order.
// Other unrelated events can be in between.
//
// messagesResBody: from a `/messages?dir=b` request (these will be in reverse-chronological order)
// eventIDs: the list of event IDs in chronological order that we expect to see in the response
func assertMessagesInTimelineInOrder(t *testing.T, messagesResBody json.RawMessage, expectedEventIDs []string) {
	t.Helper()

	wantKey := "chunk"
	keyRes := gjson.GetBytes(messagesResBody, wantKey)
	if !keyRes.Exists() {
		t.Fatalf("missing key '%s'", wantKey)
	}
	if !keyRes.IsArray() {
		t.Fatalf("key '%s' is not an array (was %s)", wantKey, keyRes.Type)
	}

	actualEvents := keyRes.Array()
	// relevantActualEvents := make([]gjson.Result, 0, len(expectedEventIDs))
	relevantActualEventIDs := make([]string, 0, len(expectedEventIDs))
	for _, event := range actualEvents {
		if slices.Contains(expectedEventIDs, event.Get("event_id").Str) {
			// relevantActualEvents = append(relevantActualEvents, event)
			relevantActualEventIDs = append(relevantActualEventIDs, event.Get("event_id").Str)
		}
	}
	// Put them in chronological order to match the expected list
	// slices.Reverse(relevantActualEvents)
	slices.Reverse(relevantActualEventIDs)

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

func getStateID(t *testing.T, c *client.CSAPI, roomID string, stateType string, stateKey string) string {
	t.Helper()

	stateRes := c.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state"})
	stateResBody := client.ParseJSON(t, stateRes)
	eventJSON := gjson.ParseBytes(stateResBody)
	if !eventJSON.IsArray() {
		t.Fatalf("expected array of state events but found %s", eventJSON.Type)
	}

	events := eventJSON.Array()

	for _, event := range events {
		if event.Get("type").Str == stateType && event.Get("state_key").Str == stateKey {
			return event.Get("event_id").Str
		}
	}

	t.Fatalf("Unable to find state event for (%s, %s). Room state: %s", stateType, stateKey, events)
	return ""
}

// awaitPartialStateJoinCompletion waits until the joined room is no longer partial-stated
func awaitPartialStateJoinCompletion(
	t *testing.T, room_id string, user *client.CSAPI,
) {
	t.Helper()

	// Use a `/members` request to wait for the room to be un-partial stated.
	// We avoid using `/sync`, as it only waits (or used to wait) for full state at
	// particular events, rather than the whole room.
	user.MustDo(
		t,
		"GET",
		[]string{"_matrix", "client", "v3", "rooms", room_id, "members"},
	)
	t.Logf("%s's partial state join to %s completed.", user.UserID, room_id)
}
