package csapi_tests

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/must"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/tidwall/gjson"
)

func TestRoomVersions(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationTwoLocalOneRemote)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")
	charlie := deployment.Client(t, "hs2", "@charlie:hs2")

	roomVersions := gomatrixserverlib.RoomVersions()

	t.Run("Parallel", func(t *testing.T) {
		// iterate over all room versions
		for v := range roomVersions {
			roomVersion := v
			// sytest: User can create and send/receive messages in a room with version $version
			t.Run(fmt.Sprintf("User can create and send/receive messages in a room with version %s", roomVersion), func(t *testing.T) {
				t.Parallel()
				roomID := createRoomSynced(t, alice, map[string]interface{}{
					"room_version": roomVersion,
				})
				alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

				res, _ := alice.MustSync(t, client.SyncReq{})
				room := res.Get("rooms.join." + client.GjsonEscape(roomID))
				ev0 := room.Get("timeline.events").Array()[0]
				must.EqualStr(t, ev0.Get("type").Str, "m.room.create", "not a m.room.create event")
				sendMessageSynced(t, alice, roomID)
			})

			userTypes := map[string]*client.CSAPI{
				"local":  bob,
				"remote": charlie,
			}
			for typ, joiner := range userTypes {
				typ := typ
				joiner := joiner

				// sytest: $user_type user can join room with version $version
				t.Run(fmt.Sprintf("%s user can join room with version %s", typ, roomVersion), func(t *testing.T) {
					t.Parallel()
					roomAlias := fmt.Sprintf("roomAlias_V%s%s", typ, roomVersion)
					t.Logf("RoomAlias: %s", roomAlias)
					roomID := createRoomSynced(t, alice, map[string]interface{}{
						"room_version":    roomVersion,
						"room_alias_name": roomAlias,
						"preset":          "public_chat",
					})
					joinRoomSynced(t, joiner, roomID, fmt.Sprintf("#%s:%s", roomAlias, "hs1"))
					_, nextBatch := joiner.MustSync(t, client.SyncReq{})
					eventID := sendMessageSynced(t, alice, roomID)
					joiner.MustSyncUntil(t, client.SyncReq{Since: nextBatch}, client.SyncTimelineHas(roomID, func(result gjson.Result) bool {
						if len(result.Array()) > 1 {
							t.Fatal("Expected a single timeline event")
						}
						must.EqualStr(t, result.Array()[0].Get("event_id").Str, eventID, "wrong event id")
						return true
					}))
				})

				// sytest: User can invite $user_type user to room with version $version
				t.Run(fmt.Sprintf("User can invite %s user to room with version %s", typ, roomVersion), func(t *testing.T) {
					t.Parallel()
					roomID := createRoomSynced(t, alice, map[string]interface{}{
						"room_version": roomVersion,
						"preset":       "private_chat",
					})
					alice.InviteRoom(t, roomID, joiner.UserID)
					joiner.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(joiner.UserID, roomID))
					joinRoomSynced(t, joiner, roomID, "")
					_, nextBatch := joiner.MustSync(t, client.SyncReq{})
					eventID := sendMessageSynced(t, alice, roomID)
					joiner.MustSyncUntil(t, client.SyncReq{Since: nextBatch}, client.SyncTimelineHas(roomID, func(result gjson.Result) bool {
						if len(result.Array()) > 1 {
							t.Fatal("Expected a single timeline event")
						}
						must.EqualStr(t, result.Array()[0].Get("event_id").Str, eventID, "wrong event id")
						return true
					}))
				})

			}

			// sytest: Remote user can backfill in a room with version $version
			t.Run(fmt.Sprintf("Remote user can backfill in a room with version %s", roomVersion), func(t *testing.T) {
				t.Parallel()
				roomID := createRoomSynced(t, alice, map[string]interface{}{
					"room_version": roomVersion,
					"invite":       []string{charlie.UserID},
				})
				for i := 0; i < 20; i++ {
					sendMessageSynced(t, alice, roomID)
				}
				charlie.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(charlie.UserID, roomID))
				joinRoomSynced(t, charlie, roomID, "")

				queryParams := url.Values{}
				queryParams.Set("dir", "b")
				queryParams.Set("limit", "6")
				res := charlie.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
				body := gjson.ParseBytes(must.ParseJSON(t, res.Body))
				defer res.Body.Close()
				if len(body.Get("chunk").Array()) != 6 {
					t.Fatal("Expected 6 messages")
				}
			})

			// sytest: Can reject invites over federation for rooms with version $version
			t.Run(fmt.Sprintf("Can reject invites over federation for rooms with version %s", roomVersion), func(t *testing.T) {
				t.Parallel()
				roomID := createRoomSynced(t, alice, map[string]interface{}{
					"room_version": roomVersion,
					"invite":       []string{charlie.UserID},
				})
				charlie.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(charlie.UserID, roomID))
				charlie.LeaveRoom(t, roomID)
			})

			// sytest: Can receive redactions from regular users over federation in room version $version
			t.Run(fmt.Sprintf("Can receive redactions from regular users over federation in room version %s", roomVersion), func(t *testing.T) {
				t.Parallel()
				roomID := createRoomSynced(t, alice, map[string]interface{}{
					"room_version": roomVersion,
					"invite":       []string{charlie.UserID},
				})
				charlie.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(charlie.UserID, roomID))
				joinRoomSynced(t, charlie, roomID, "")
				eventID := sendMessageSynced(t, charlie, roomID)
				// redact the message
				res := charlie.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "redact", eventID}, client.WithRawBody([]byte("{}")))
				js := must.ParseJSON(t, res.Body)
				defer res.Body.Close()
				redactID := must.GetJSONFieldStr(t, js, "event_id")
				alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(result gjson.Result) bool {
					return redactID == result.Get("event_id").Str
				}))
				// query messages
				queryParams := url.Values{}
				queryParams.Set("dir", "b")
				res = alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
				body := gjson.ParseBytes(must.ParseJSON(t, res.Body))
				defer res.Body.Close()
				events := body.Get("chunk").Array()
				// first event should be the redaction
				must.EqualStr(t, events[0].Get("event_id").Str, redactID, "wrong event")
				must.EqualStr(t, events[0].Get("redacts").Str, eventID, "wrong event")
				// second event should be the original event
				must.EqualStr(t, events[1].Get("event_id").Str, eventID, "wrong event")
				must.EqualStr(t, events[1].Get("unsigned.redacted_by").Str, redactID, "wrong event")
			})
		}
	})
}

func sendMessageSynced(t *testing.T, cl *client.CSAPI, roomID string) (eventID string) {
	return cl.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "hello world",
		},
	})
}

func joinRoomSynced(t *testing.T, cl *client.CSAPI, roomID, alias string) {
	joinRoom := roomID
	if alias != "" {
		joinRoom = alias
	}
	cl.JoinRoom(t, joinRoom, []string{})
	cl.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(cl.UserID, roomID))
}

func createRoomSynced(t *testing.T, c *client.CSAPI, content map[string]interface{}) (roomID string) {
	t.Helper()
	roomID = c.CreateRoom(t, content)
	c.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(c.UserID, roomID))
	return
}
