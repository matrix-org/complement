package csapi_tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestSyncTimeline(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")

	reqBody, err := json.Marshal(map[string]interface{}{
		"room": map[string]interface{}{
			"timeline": map[string]interface{}{
				"limit": 1,
			},
		},
	})
	if err != nil {
		t.Fatalf("unable to marshal filter: %v", err)
	}

	filterLimit1ID := createFilter(t, alice, reqBody, alice.UserID)

	t.Run("Parallel", func(t *testing.T) {
		// sytest: Can sync a room with a single message
		t.Run("Can sync a room with a single message", func(t *testing.T) {
			t.Parallel()
			reqBody, err = json.Marshal(map[string]interface{}{
				"room": map[string]interface{}{
					"timeline": map[string]int{
						"limit": 2,
					},
				},
			})
			if err != nil {
				t.Fatalf("unable to marshal filter: %v", err)
			}

			filterID := createFilter(t, alice, reqBody, alice.UserID)
			roomID := alice.CreateRoom(t, struct{}{})
			eventIDs := sendMessages(t, alice, roomID, "m.room.message", "Test message ", 2)
			res, _ := alice.MustSync(t, client.SyncReq{Filter: filterID})
			room := res.Get("rooms.join." + client.GjsonEscape(roomID))
			if !room.Get("timeline").Exists() || !room.Get("state").Exists() || !room.Get("ephemeral").Exists() {
				t.Fatalf("missing required field in response")
			}
			timeline := room.Get("timeline")
			if !timeline.Get("events").Exists() || !timeline.Get("limited").Exists() || !timeline.Get("prev_batch").Exists() {
				t.Fatalf("missing required field in timeline")
			}
			events := timeline.Get("events").Array()
			wantEventCount := 2
			assertEqual(t, len(events), wantEventCount)
			assertEqual(t, events[0].Get("event_id").Str, eventIDs[0])
			assertEqual(t, events[0].Get("content.body").Str, "Test message 0")
			assertEqual(t, events[1].Get("event_id").Str, eventIDs[1])
			assertEqual(t, events[1].Get("content.body").Str, "Test message 1")
			assertEqual(t, timeline.Get("limited").Bool(), true)
		})

		// sytest: Can sync a room with a message with a transaction id
		t.Run("Can sync a room with a message with a transaction id", func(t *testing.T) {
			t.Parallel()
			reqBody, err = json.Marshal(map[string]interface{}{
				"room": map[string]interface{}{
					"timeline": map[string]interface{}{
						"limit": 1,
					},
					"state": map[string][]string{
						"types": {},
					},
				},
				"presence": map[string][]string{
					"types": {},
				},
			})
			if err != nil {
				t.Fatalf("unable to marshal filter: %v", err)
			}

			filterID := createFilter(t, alice, reqBody, alice.UserID)
			roomID := alice.CreateRoom(t, struct{}{})
			txnID := "my_transaction_id"
			eventID := alice.SendEventSynced(t, roomID, b.Event{
				Sender:        alice.UserID,
				Type:          "m.room.message",
				TransactionID: &txnID,
				Content: map[string]interface{}{
					"body":    "A test message",
					"msgtype": "m.text",
				},
			})

			alice.MustSyncUntil(t, client.SyncReq{Filter: filterID}, client.SyncTimelineHasEventID(roomID, eventID))
			res, _ := alice.MustSync(t, client.SyncReq{Filter: filterID})
			room := res.Get("rooms.join." + client.GjsonEscape(roomID))
			wantEventCount := 1
			events := room.Get("timeline.events").Array()
			assertEqual(t, len(events), wantEventCount)
			assertEqual(t, events[0].Get("content.body").Str, "A test message")
			assertEqual(t, events[0].Get("unsigned.transaction_id").Str, txnID)
			assertEqual(t, room.Get("timeline.limited").Bool(), true)
		})

		// sytest: A message sent after an initial sync appears in the timeline of an incremental sync.
		t.Run("A message sent after an initial sync appears in the timeline of an incremental sync.", func(t *testing.T) {
			t.Parallel()
			reqBody, err = json.Marshal(map[string]interface{}{
				"room": map[string]interface{}{
					"timeline": map[string]interface{}{
						"limit": 1,
					},
					"state": map[string][]string{
						"types": {},
					},
				},
				"presence": map[string][]string{
					"types": {},
				},
			})
			if err != nil {
				t.Fatalf("unable to marshal filter: %v", err)
			}

			filterID := createFilter(t, alice, reqBody, alice.UserID)
			roomID := alice.CreateRoom(t, struct{}{})
			_, since := alice.MustSync(t, client.SyncReq{Filter: filterID})

			txnID := "my_transaction_id2"
			eventID := alice.SendEventSynced(t, roomID, b.Event{
				Sender:        alice.UserID,
				Type:          "m.room.message",
				TransactionID: &txnID,
				Content: map[string]interface{}{
					"body":    "A test message",
					"msgtype": "m.text",
				},
			})

			res, _ := alice.MustSync(t, client.SyncReq{Filter: filterID, Since: since})
			room := res.Get("rooms.join." + client.GjsonEscape(roomID))
			wantEventCount := 1
			events := room.Get("timeline.events").Array()
			assertEqual(t, len(events), wantEventCount)
			assertEqual(t, events[0].Get("event_id").Str, eventID)
			assertEqual(t, events[0].Get("content.body").Str, "A test message")
			assertEqual(t, events[0].Get("unsigned.transaction_id").Str, txnID)
			assertEqual(t, room.Get("timeline.limited").Bool(), false)
		})

		// sytest: A filtered timeline reaches its limit
		t.Run("A filtered timeline reaches its limit", func(t *testing.T) {
			t.Parallel()
			reqBody, err = json.Marshal(map[string]interface{}{
				"room": map[string]interface{}{
					"timeline": map[string]interface{}{
						"limit": 1,
						"types": []string{"m.room.message"},
					},
				},
				"presence": map[string][]string{
					"types": {},
				},
				"account_data": map[string][]string{
					"types": {},
				},
			})
			if err != nil {
				t.Fatalf("unable to marshal filter: %v", err)
			}

			filterID := createFilter(t, alice, reqBody, alice.UserID)
			roomID := alice.CreateRoom(t, struct{}{})

			eventID := alice.SendEventSynced(t, roomID, b.Event{
				Sender: alice.UserID,
				Type:   "m.room.message",
				Content: map[string]interface{}{
					"body":    "A test message",
					"msgtype": "m.text",
				},
			})

			sendMessages(t, alice, roomID, "a.made.up.filler.type", "Test message ", 12)

			res, _ := alice.MustSync(t, client.SyncReq{Filter: filterID})
			room := res.Get("rooms.join." + client.GjsonEscape(roomID))
			wantEventCount := 1
			events := room.Get("timeline.events").Array()
			assertEqual(t, len(events), wantEventCount)
			assertEqual(t, events[0].Get("event_id").Str, eventID)
			assertEqual(t, events[0].Get("content.body").Str, "A test message")
			assertEqual(t, room.Get("timeline.limited").Bool(), false)
		})

		// sytest: Syncing a new room with a large timeline limit isn't limited
		t.Run("Syncing a new room with a large timeline limit isn't limited", func(t *testing.T) {
			t.Parallel()
			reqBody, err = json.Marshal(map[string]interface{}{
				"room": map[string]interface{}{
					"timeline": map[string]interface{}{
						"limit": 100,
					},
				},
			})
			if err != nil {
				t.Fatalf("unable to marshal filter: %v", err)
			}

			filterID := createFilter(t, alice, reqBody, alice.UserID)
			roomID := alice.CreateRoom(t, struct{}{})

			res, _ := alice.MustSync(t, client.SyncReq{Filter: filterID})
			room := res.Get("rooms.join." + client.GjsonEscape(roomID))
			assertEqual(t, room.Get("timeline.limited").Bool(), false)
		})

		// sytest: A full_state incremental update returns only recent timeline
		t.Run("A full_state incremental update returns only recent timeline", func(t *testing.T) {
			t.Parallel()

			roomID := alice.CreateRoom(t, struct{}{})
			_, since := alice.MustSync(t, client.SyncReq{Filter: filterLimit1ID})

			sendMessages(t, alice, roomID, "a.made.up.filler.type", "Test message ", 11)
			sendMessages(t, alice, roomID, "another.filler.type", "Test message ", 1)

			res, _ := alice.MustSync(t, client.SyncReq{Filter: filterLimit1ID, Since: since, FullState: true})

			room := res.Get("rooms.join." + client.GjsonEscape(roomID))
			wantEventCount := 1
			events := room.Get("timeline.events").Array()
			assertEqual(t, len(events), wantEventCount)
			assertEqual(t, events[0].Get("type").Str, "another.filler.type")
		})

		// sytest: A prev_batch token can be used in the v1 messages API
		t.Run("A prev_batch token can be used in the v1 messages API", func(t *testing.T) {
			t.Parallel()

			roomID := alice.CreateRoom(t, struct{}{})

			eventIDs := sendMessages(t, alice, roomID, "m.room.message", "Test message ", 2)

			res, _ := alice.MustSync(t, client.SyncReq{Filter: filterLimit1ID})

			room := res.Get("rooms.join." + client.GjsonEscape(roomID))
			wantEventCount := 1
			events := room.Get("timeline.events").Array()
			assertEqual(t, len(events), wantEventCount)
			assertEqual(t, events[0].Get("event_id").Str, eventIDs[len(eventIDs)-1])
			assertEqual(t, events[0].Get("content.body").Str, "Test message 1")
			assertEqual(t, room.Get("timeline.limited").Bool(), true)

			queryParams := url.Values{}
			queryParams.Set("dir", "b")
			queryParams.Set("from", room.Get("timeline.prev_batch").Str)
			queryParams.Set("limit", "1")
			messagesRes := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
			must.MatchResponse(t, messagesRes, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONArrayEach("chunk", func(r gjson.Result) error {
						if r.Get("type").Str == "m.room.message" && r.Get("content.body").Str == "Test message 0" {
							return nil
						}
						return fmt.Errorf("did not find correct event")
					}),
				},
			})
		})

		// sytest: A prev_batch token from incremental sync can be used in the v1 messages API
		t.Run("A prev_batch token from incremental sync can be used in the v1 messages API", func(t *testing.T) {
			t.Parallel()

			roomID := alice.CreateRoom(t, struct{}{})

			eventIDs1 := sendMessages(t, alice, roomID, "m.room.message", "Test message ", 1)
			_, since := alice.MustSync(t, client.SyncReq{Filter: filterLimit1ID})

			eventIDs2 := sendMessages(t, alice, roomID, "m.room.message", "Test message ", 1)
			res, _ := alice.MustSync(t, client.SyncReq{Filter: filterLimit1ID, Since: since})

			eventIDs := append(eventIDs1, eventIDs2...)
			room := res.Get("rooms.join." + client.GjsonEscape(roomID))
			wantEventCount := 1
			events := room.Get("timeline.events").Array()
			assertEqual(t, len(events), wantEventCount)
			assertEqual(t, events[0].Get("event_id").Str, eventIDs[len(eventIDs)-1])

			queryParams := url.Values{}
			queryParams.Set("dir", "b")
			queryParams.Set("from", room.Get("timeline.prev_batch").Str)
			queryParams.Set("limit", "1")
			messagesRes := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
			must.MatchResponse(t, messagesRes, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONArrayEach("chunk", func(r gjson.Result) error {
						if r.Get("type").Str == "m.room.message" && r.Get("content.body").Str == "Test message 0" && r.Get("event_id").Str == eventIDs[0] {
							return nil
						}
						return fmt.Errorf("did not find correct event")
					}),
				},
			})
		})
		// sytest: A next_batch token can be used in the v1 messages API
		t.Run("A next_batch token can be used in the v1 messages API", func(t *testing.T) {
			t.Parallel()

			roomID := alice.CreateRoom(t, struct{}{})

			eventIDs1 := sendMessages(t, alice, roomID, "m.room.message", "Test message ", 1)
			res, _ := alice.MustSync(t, client.SyncReq{Filter: filterLimit1ID})

			room := res.Get("rooms.join." + client.GjsonEscape(roomID))
			wantEventCount := 1
			events := room.Get("timeline.events").Array()
			assertEqual(t, len(events), wantEventCount)
			assertEqual(t, events[0].Get("event_id").Str, eventIDs1[len(eventIDs1)-1])

			eventIDs2 := sendMessages(t, alice, roomID, "m.room.message", "Test message ", 1)
			eventIDs := append(eventIDs1, eventIDs2...)

			queryParams := url.Values{}
			queryParams.Set("dir", "f")
			queryParams.Set("from", room.Get("timeline.next_batch").Str)
			queryParams.Set("limit", "1")
			messagesRes := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
			must.MatchResponse(t, messagesRes, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONArrayEach("chunk", func(r gjson.Result) error {
						if r.Get("type").Str == "m.room.message" && r.Get("content.body").Str == "Test message 0" && r.Get("event_id").Str == eventIDs[len(eventIDs)-1] {
							return nil
						}
						return fmt.Errorf("did not find correct event")
					}),
				},
			})
		})
	})
}

func assertEqual(t *testing.T, got, want interface{}) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected value: %v, want %v", got, want)
	}
}
