package tests

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestFederationMessages(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs2", "@bob:hs2")

	t.Run("Parallel", func(t *testing.T) {
		// sytest: Remote room members also see posted message events
		t.Run("Remote room members also see posted message events", func(t *testing.T) {
			t.Parallel()
			roomID := createRoom(t, alice, bob)
			eventIDs := sendMessages(t, alice, roomID, "m.room.message", "Test message ", 1)
			bob.MustSyncUntil(t, client.SyncReq{},
				client.SyncTimelineHasEventID(roomID, eventIDs[0]),
				func(clientUserID string, syncRes gjson.Result) error {
					evs := syncRes.Get("rooms.join." + client.GjsonEscape(roomID) + ".timeline.events").Array()
					if len(evs) == 0 {
						return fmt.Errorf("no timeline events returned")
					}
					if evs[0].Get("type").Str != "m.room.message" {
						return fmt.Errorf("wrong event type")
					}
					must.EqualStr(t, evs[len(evs)-1].Get("content.msgtype").Str, "m.text", "wrong message type")
					must.EqualStr(t, evs[len(evs)-1].Get("content.body").Str, "Test message 0", "wrong message body")
					must.EqualStr(t, evs[len(evs)-1].Get("sender").Str, alice.UserID, "wrong sender")
					must.EqualStr(t, evs[len(evs)-1].Get("event_id").Str, eventIDs[0], "wrong event_id")
					return nil
				},
			)
		})

		// sytest: Remote room members can get room messages
		t.Run("Remote room members can get room messages", func(t *testing.T) {
			t.Parallel()
			roomID := createRoom(t, alice, bob)
			eventIDs := sendMessages(t, alice, roomID, "m.room.message", "Test message ", 1)

			queryParams := url.Values{}
			queryParams.Set("dir", "f")
			queryParams.Set("limit", "1")
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, eventIDs[0]))
			res := bob.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
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
		})

		// sytest: Message history can be paginated over federation
		t.Run("Message history can be paginated over federation", func(t *testing.T) {
			t.Parallel()
			roomID := createRoom(t, alice, bob)
			eventIDs := sendMessages(t, alice, roomID, "m.room.message", "Test message ", 20)

			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, eventIDs[len(eventIDs)-1]))

			queryParams := url.Values{}
			queryParams.Set("dir", "b")
			queryParams.Set("limit", "5")

			res := bob.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithQueries(queryParams))

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
			res = bob.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
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
			sendMessages(t, bob, roomID, "m.room.message", "testing", 1)
		})
	})
}

func createRoom(t *testing.T, alice, bob *client.CSAPI) string {
	t.Helper()
	roomID := alice.CreateRoom(t, map[string]interface{}{
		"invite": []string{bob.UserID},
	})
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
	bob.JoinRoom(t, roomID, []string{})
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
	return roomID
}

func sendMessages(t *testing.T, client *client.CSAPI, roomID string, msgType string, prefix string, count int) []string {
	t.Helper()
	eventIDs := make([]string, 0, count)
	for i := 0; i < count; i++ {
		eventID := client.SendEventSynced(t, roomID, b.Event{
			Sender: client.UserID,
			Type:   msgType,
			Content: map[string]interface{}{
				"body":    fmt.Sprintf("%s%d", prefix, i),
				"msgtype": "m.text",
			},
		})
		eventIDs = append(eventIDs, eventID)
	}
	return eventIDs
}
