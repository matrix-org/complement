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

			bob.SyncUntilTimelineHas(
				t,
				roomID,
				func(ev gjson.Result) bool {
					if ev.Get("type").Str != "m.room.member" || ev.Get("state_key").Str != bob.UserID {
						return false
					}
					must.EqualStr(t, ev.Get("content").Get("membership").Str, "join", "Bob failed to join the room")
					return true
				},
			)
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

			bob.SyncUntilTimelineHas(
				t,
				roomID,
				func(ev gjson.Result) bool {
					if ev.Get("type").Str != "m.room.member" || ev.Get("state_key").Str != bob.UserID {
						return false
					}
					must.EqualStr(t, ev.Get("content").Get("membership").Str, "join", "Bob failed to join the room")
					return true
				},
			)
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

			bob.SyncUntilTimelineHas(
				t,
				roomID,
				func(ev gjson.Result) bool {
					if ev.Get("type").Str != "m.room.member" || ev.Get("state_key").Str != bob.UserID {
						return false
					}
					must.EqualStr(t, ev.Get("content").Get("membership").Str, "join", "Bob failed to join the room")
					return true
				},
			)
		})
		// sytest: POST /join/:room_id can join a room with custom content
		t.Run("POST /join/:room_id can join a room with custom content", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
			})

			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"foo": "bar",
			})

			res := bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "join", roomID}, reqBody)

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("room_id"),
					match.JSONKeyTypeEqual("room_id", gjson.String),
					match.JSONKeyEqual("room_id", roomID),
				},
			})

			bob.SyncUntilTimelineHas(
				t,
				roomID,
				func(ev gjson.Result) bool {
					if ev.Get("type").Str != "m.room.member" || ev.Get("state_key").Str != bob.UserID {
						return false
					}
					must.EqualStr(t, ev.Get("content").Get("membership").Str, "join", "Bob failed to join the room")
					must.EqualStr(t, ev.Get("content").Get("foo").Str, "bar", "Failed to propagate custom content")
					return true
				},
			)
		})
		// sytest: POST /join/:room_alias can join a room with custom content
		t.Run("POST /join/:room_alias can join a room with custom content", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"visibility":      "public",
				"preset":          "public_chat",
				"room_alias_name": "room_alias_random_2",
			})

			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"foo": "bar",
			})

			res := bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "join", "#room_alias_random_2:hs1"}, reqBody)

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("room_id"),
					match.JSONKeyTypeEqual("room_id", gjson.String),
					match.JSONKeyEqual("room_id", roomID),
				},
			})

			bob.SyncUntilTimelineHas(
				t,
				roomID,
				func(ev gjson.Result) bool {
					if ev.Get("type").Str != "m.room.member" || ev.Get("state_key").Str != bob.UserID {
						return false
					}
					must.EqualStr(t, ev.Get("content").Get("membership").Str, "join", "Bob failed to join the room")
					must.EqualStr(t, ev.Get("content").Get("foo").Str, "bar", "Failed to propagate custom content")
					return true
				},
			)
		})
		// sytest: POST /rooms/:room_id/leave can leave a room
		t.Run("POST /rooms/:room_id/leave can leave a room", func(t *testing.T) {
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

			bob.SyncUntilTimelineHas(
				t,
				roomID,
				func(ev gjson.Result) bool {
					if ev.Get("type").Str != "m.room.member" || ev.Get("state_key").Str != bob.UserID {
						return false
					}
					must.EqualStr(t, ev.Get("content").Get("membership").Str, "join", "Bob failed to join the room")
					return true
				},
			)

			_ = bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "rooms", roomID, "leave"})

			bob.SyncUntilTimelineHas(
				t,
				roomID,
				func(ev gjson.Result) bool {
					if ev.Get("type").Str != "m.room.member" || ev.Get("state_key").Str != bob.UserID {
						return false
					}
					if ev.Get("content").Get("membership").Str == "join" {
						t.Fatal("expected membership not equal to join")
						return false
					}
					return true
				},
			)
		})
	})
}
