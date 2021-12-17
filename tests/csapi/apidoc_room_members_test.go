package csapi_tests

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestRoomMembers(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")
	t.Run("Parallel", func(t *testing.T) {
		// sytest: POST /rooms/:room_id/join can join a room
		t.Run("POST /rooms/:room_id/join can join a room", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
			})

			res := bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "rooms", roomID, "join"})

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("room_id"),
					match.JSONKeyTypeEqual("room_id", gjson.String),
					match.JSONKeyEqual("room_id", roomID),
				},
			})

			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
		})
		// sytest: POST /join/:room_alias can join a room
		t.Run("POST /join/:room_alias can join a room", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"visibility":      "public",
				"preset":          "public_chat",
				"room_alias_name": "room_alias_random",
			})

			res := bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "join", "#room_alias_random:hs1"})

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("room_id"),
					match.JSONKeyTypeEqual("room_id", gjson.String),
					match.JSONKeyEqual("room_id", roomID),
				},
			})

			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
		})
		// sytest: POST /join/:room_id can join a room
		t.Run("POST /join/:room_id can join a room", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
			})

			res := bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "join", roomID})

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("room_id"),
					match.JSONKeyTypeEqual("room_id", gjson.String),
					match.JSONKeyEqual("room_id", roomID),
				},
			})

			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(
				roomID,
				func(ev gjson.Result) bool {
					if ev.Get("type").Str != "m.room.member" || ev.Get("state_key").Str != bob.UserID {
						return false
					}
					must.EqualStr(t, ev.Get("content").Get("membership").Str, "join", "Bob failed to join the room")
					return true
				},
			))
		})
		// sytest: Test that we can be reinvited to a room we created
		t.Run("Test that we can be reinvited to a room we created", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})

			alice.InviteRoom(t, roomID, bob.UserID)

			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))

			bob.JoinRoom(t, roomID, nil)

			// Sync to make sure bob has joined
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

			stateKey := ""
			alice.SendEventSynced(t, roomID, b.Event{
				Type:     "m.room.power_levels",
				StateKey: &stateKey,
				Content: map[string]interface{}{
					"invite": 100,
					"users": map[string]interface{}{
						alice.UserID: 100,
						bob.UserID:   100,
					},
				},
			})

			alice.LeaveRoom(t, roomID)

			// Wait until alice has left the room
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(
				roomID,
				func(ev gjson.Result) bool {
					return ev.Get("type").Str == "m.room.member" &&
						ev.Get("content.membership").Str == "leave" &&
						ev.Get("state_key").Str == alice.UserID
				},
			))

			bob.InviteRoom(t, roomID, alice.UserID)
			since := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, roomID))
			alice.JoinRoom(t, roomID, nil)
			alice.MustSyncUntil(t, client.SyncReq{Since: since}, client.SyncJoinedTo(alice.UserID, roomID))
		})
	})
}
