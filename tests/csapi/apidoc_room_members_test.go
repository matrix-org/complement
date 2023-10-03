package csapi_tests

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/internal/b"
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

			res := bob.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "join"})

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

			res := bob.MustDo(t, "POST", []string{"_matrix", "client", "v3", "join", "#room_alias_random:hs1"})

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

			res := bob.MustDo(t, "POST", []string{"_matrix", "client", "v3", "join", roomID})

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
		// sytest: POST /join/:room_id can join a room with custom content
		t.Run("POST /join/:room_id can join a room with custom content", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
				"room_alias": "helloWorld",
			})

			joinBody := client.WithJSONBody(t, map[string]string{"foo": "bar"})

			res := bob.MustDo(t, "POST", []string{"_matrix", "client", "v3", "join", roomID}, joinBody)

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("room_id"),
					match.JSONKeyTypeEqual("room_id", gjson.String),
					match.JSONKeyEqual("room_id", roomID),
				},
			})

			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
			res = alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.member", bob.UserID})

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyEqual("foo", "bar"),
					match.JSONKeyEqual("membership", "join"),
				},
			})
		})
		// sytest: POST /join/:room_alias can join a room with custom content
		t.Run("POST /join/:room_alias can join a room with custom content", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"visibility":      "public",
				"preset":          "public_chat",
				"room_alias_name": "room_alias_random2",
			})
			joinBody := client.WithJSONBody(t, map[string]string{"foo": "bar"})
			res := bob.MustDo(t, "POST", []string{"_matrix", "client", "v3", "join", "#room_alias_random2:hs1"}, joinBody)

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("room_id"),
					match.JSONKeyTypeEqual("room_id", gjson.String),
					match.JSONKeyEqual("room_id", roomID),
				},
			})

			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
			res = alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.member", bob.UserID})

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyEqual("foo", "bar"),
					match.JSONKeyEqual("membership", "join"),
				},
			})
		})

		// sytest: POST /rooms/:room_id/ban can ban a user
		t.Run("POST /rooms/:room_id/ban can ban a user", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
			})

			bob.MustDo(t, "POST", []string{"_matrix", "client", "v3", "join", roomID})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

			// ban bob from room
			banBody := client.WithJSONBody(t, map[string]string{
				"user_id": bob.UserID,
				"reason":  "Testing",
			})
			res := alice.Do(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "ban"}, banBody)
			must.MatchResponse(t, res, match.HTTPResponse{StatusCode: 200})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
				if ev.Get("type").Str != "m.room.member" || ev.Get("state_key").Str != bob.UserID {
					return false
				}
				return ev.Get("content.membership").Str == "ban"
			}))
			// verify bob is banned
			res = alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.member", bob.UserID})
			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyEqual("membership", "ban"),
				},
			})
		})

		// sytest: POST /rooms/:room_id/invite can send an invite
		t.Run("POST /rooms/:room_id/invite can send an invite", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{})
			alice.InviteRoom(t, roomID, bob.UserID)
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			res := alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.member", bob.UserID})
			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyEqual("membership", "invite"),
				},
			})
		})

		// sytest: POST /rooms/:room_id/leave can leave a room
		t.Run("POST /rooms/:room_id/leave can leave a room", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{})
			alice.InviteRoom(t, roomID, bob.UserID)
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			bob.JoinRoom(t, roomID, []string{})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
			bob.LeaveRoom(t, roomID)
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(bob.UserID, roomID))

			res := alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.member", bob.UserID})
			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyEqual("membership", "leave"),
				},
			})
		})
	})
}
