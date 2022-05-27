package csapi_tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/matrix-org/gomatrixserverlib"
	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/matrix-org/complement/runtime"
)

func TestRoomsMessages(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	t.Run("Parallel", func(t *testing.T) {
		// sytest: Local room members see posted message events
		t.Run("Local room members see posted message events", func(t *testing.T) {
			t.Parallel()
			roomID := createRoom(t, alice, bob)
			eventIDs := sendMessages(t, alice, roomID, "m.room.message", "Test message ", 1)
			bob.MustSyncUntil(t, client.SyncReq{},
				func(clientUserID string, syncRes gjson.Result) error {
					evs := syncRes.Get("rooms.join." + client.GjsonEscape(roomID) + ".timeline.events").Array()
					if len(evs) == 0 {
						return fmt.Errorf("no timeline events returned")
					}
					must.EqualStr(t, evs[len(evs)-1].Get("content.msgtype").Str, "m.text", "wrong message type")
					must.EqualStr(t, evs[len(evs)-1].Get("content.body").Str, "Test message 0", "wrong message body")
					must.EqualStr(t, evs[len(evs)-1].Get("sender").Str, alice.UserID, "wrong sender")
					must.EqualStr(t, evs[len(evs)-1].Get("event_id").Str, eventIDs[0], "wrong event_id")
					return nil
				},
			)
		})

		// sytest: Fetching eventstream a second time doesn't yield the message again
		t.Run("Fetching eventstream a second time doesn't yield the message again", func(t *testing.T) {
			t.Parallel()
			roomID := createRoom(t, alice, bob)
			eventIDs := sendMessages(t, alice, roomID, "m.room.message", "Test message ", 1)
			since := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, eventIDs[0]))
			res, _ := bob.MustSync(t, client.SyncReq{Since: since})
			if len(res.Get("rooms.join."+client.GjsonEscape(roomID)+".timeline.events").Array()) > 0 {
				t.Fatalf("expected no timline events")
			}
		})

		// sytest: Local non-members don't see posted message events
		t.Run("Local non-members don't see posted message events", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, struct{}{})
			sendMessages(t, alice, roomID, "m.room.message", "Test message ", 1)
			resChan := make(chan struct{})
			doneChan := make(chan struct{})
			go func() {
				for {
					res, _ := bob.MustSync(t, client.SyncReq{TimeoutMillis: "1000"})
					if res.Get("rooms.join." + client.GjsonEscape(roomID)).Exists() {
						resChan <- struct{}{}
						return
					}
					select {
					case <-doneChan:
						return
					default:
					}
				}
			}()
			select {
			case <-resChan:
				t.Fatalf("received an unexpected event")
			case <-time.After(time.Millisecond * 500): // wait for 500ms for an event to arrive
				doneChan <- struct{}{}
				close(doneChan)
				close(resChan)
			}
		})

		// sytest: Local room members can get room messages
		t.Run("Local room members can get room messages", func(t *testing.T) {
			t.Parallel()
			roomID := createRoom(t, alice, bob)
			eventIDs := sendMessages(t, alice, roomID, "m.room.message", "Test message ", 1)

			queryParams := url.Values{}
			queryParams.Set("dir", "f")
			queryParams.Set("limit", "1")

			for _, user := range []*client.CSAPI{alice, bob} {
				res := user.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
				must.MatchResponse(t, res, match.HTTPResponse{
					StatusCode: http.StatusOK,
					JSON: []match.JSON{
						match.JSONKeyPresent("start"),
						match.JSONKeyPresent("end"),
						match.JSONArrayEach("chunk", func(r gjson.Result) error {
							if r.Get("type").Str == "m.room.message" &&
								r.Get("content.body").Str == "Test message 0" &&
								r.Get("event_id").Str == eventIDs[0] &&
								r.Get("room_id").Str == roomID {
								return nil
							}
							return fmt.Errorf("did not find correct event")
						}),
					},
				})
			}
		})

		// sytest: Message history can be paginated
		t.Run("Message history can be paginated", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, struct{}{})
			sendMessages(t, alice, roomID, "m.room.message", "Test message ", 20)

			queryParams := url.Values{}
			queryParams.Set("dir", "b")
			queryParams.Set("limit", "5")

			res := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithQueries(queryParams))

			wantMsgNumber := 19
			body := must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONKeyPresent("start"),
					match.JSONKeyPresent("end"),
					match.JSONArrayEach("chunk", func(r gjson.Result) error {
						if r.Get("type").Str == "m.room.message" &&
							r.Get("content.body").Str == "Test message "+strconv.Itoa(wantMsgNumber) {
							wantMsgNumber--
							return nil
						}
						return fmt.Errorf("did not find correct event")
					}),
				},
			})
			if len(gjson.GetBytes(body, "chunk").Array()) > 5 {
				t.Fatalf("unexpected event count")
			}
			queryParams.Set("from", must.GetJSONFieldStr(t, body, "end"))
			res = alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONKeyPresent("start"),
					match.JSONKeyPresent("end"),
					match.JSONArrayEach("chunk", func(r gjson.Result) error {
						if r.Get("type").Str == "m.room.message" &&
							r.Get("content.body").Str == "Test message "+strconv.Itoa(wantMsgNumber) {
							wantMsgNumber--
							return nil
						}
						return fmt.Errorf("did not find correct event")
					}),
				},
			})
		})

		// sytest: Ephemeral messages received from clients are correctly expired
		t.Run("Ephemeral messages received from clients are correctly expired", func(t *testing.T) {
			runtime.SkipIf(t, runtime.Dendrite) // not implemented yet
			t.Parallel()
			roomID := alice.CreateRoom(t, struct{}{})
			eventID := alice.SendEventSynced(t, roomID, b.Event{
				Sender: alice.UserID,
				Type:   "m.room.message",
				Content: map[string]interface{}{
					"body":                           "test message",
					"msgtype":                        "m.text",
					"org.matrix.self_destruct_after": gomatrixserverlib.AsTimestamp(time.Now().Add(time.Second)),
				},
			})
			// wait for the message to expire
			time.Sleep(time.Second * 2)

			reqBody, err := json.Marshal(map[string][]string{
				"types": {"m.room.message"},
			})
			if err != nil {
				t.Fatalf("unable to marshal filter: %v", err)
			}
			queryParams := url.Values{}
			queryParams.Set("dir", "b")
			queryParams.Set("filter", string(reqBody))

			res := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONKeyPresent("start"),
					match.JSONKeyPresent("end"),
					match.JSONArrayEach("chunk", func(r gjson.Result) error {
						if r.Get("event_id").Str == eventID {
							if r.Get("content.body").Str == "test message" {
								return fmt.Errorf("content is still visible")
							}
							return nil
						}
						return fmt.Errorf("did not find correct event")
					}),
				},
			})
		})
	})
}

func createRoom(t *testing.T, alice, bob *client.CSAPI) string {
	t.Helper()
	roomID := alice.CreateRoom(t, map[string]interface{}{
		"invite": []string{bob.UserID},
	})
	bob.JoinRoom(t, roomID, []string{})
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
	return roomID
}
