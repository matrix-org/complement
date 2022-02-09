package csapi_tests

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
)

// These tests ensure that forgetting about rooms works as intended
//
func TestRoomForget(t *testing.T) {
	deployment := Deploy(t, b.BlueprintCleanHS)
	defer deployment.Destroy(t)

	alice := deployment.RegisterUser(t, "hs1", "alice", "sufficiently_long_password_alice")
	bob := deployment.RegisterUser(t, "hs1", "bob", "sufficiently_long_password_bob")
	deployment.Config.AlwaysPrintServerLogs = true
	t.Run("Parallel", func(t *testing.T) {
		// sytest: Can't forget room you're still in
		t.Run("Can't forget room you're still in", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "private_chat"})
			res := alice.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "rooms", roomID, "forget"})
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusBadRequest,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_UNKNOWN"),
				},
			})
		})
		// sytest: Forgotten room messages cannot be paginated
		t.Run("Forgotten room messages cannot be paginated", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})
			bob.JoinRoom(t, roomID, []string{})
			alice.SendEventSynced(t, roomID, b.Event{
				Type: "m.room.message",
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    "Hello world!",
				},
			})
			alice.LeaveRoom(t, roomID)
			alice.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "rooms", roomID, "forget"})
			res := alice.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"})
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusForbidden,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_FORBIDDEN"),
				},
			})
		})
		// sytest: Forgetting room does not show up in v2 /sync
		t.Run("Forgetting room does not show up in v2 /sync", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})
			bob.JoinRoom(t, roomID, []string{})
			alice.SendEventSynced(t, roomID, b.Event{
				Type: "m.room.message",
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    "Hello world!",
				},
			})
			alice.LeaveRoom(t, roomID)
			alice.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "rooms", roomID, "forget"})
			result, _ := alice.MustSync(t, client.SyncReq{})
			if result.Get("rooms.archived").Get(roomID).Exists() {
				t.Errorf("Did not expect room in archived")
			}
			if result.Get("rooms.join").Get(roomID).Exists() {
				t.Errorf("Did not expect room in joined")
			}
			if result.Get("rooms.invite").Get(roomID).Exists() {
				t.Errorf("Did not expect room in invited")
			}
		})
		// sytest: Can forget room you've been kicked from
		t.Run("Can forget room you've been kicked from", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})
			bob.JoinRoom(t, roomID, []string{})
			alice.SendEventSynced(t, roomID, b.Event{
				Type: "m.room.message",
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    "Hello world!",
				},
			})
			// Kick Bob
			alice.MustDoFunc(
				t,
				"POST",
				[]string{"_matrix", "client", "r0", "rooms", roomID, "kick"},
				client.WithJSONBody(t, map[string]interface{}{
					"user_id": bob.UserID,
				}))
			result, _ := bob.MustSync(t, client.SyncReq{})
			if result.Get("rooms.archived").Get(roomID).Exists() {
				t.Errorf("Did not expect room in archived")
			}
			if result.Get("rooms.join").Get(roomID).Exists() {
				t.Errorf("Did not expect room in joined")
			}
			if result.Get("rooms.invite").Get(roomID).Exists() {
				t.Errorf("Did not expect room in invited")
			}
		})

		// sytest: Can re-join room if re-invited
		t.Run("Can re-join room if re-invited", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset":       "public_chat",
				"room_version": "8",
				"initial_state": []map[string]interface{}{
					{
						"type":      "m.room.join_rules",
						"state_key": "",
						"content": map[string]interface{}{
							"join_rule": "invite",
						},
					},
				},
			})
			// Invite Bob
			alice.InviteRoom(t, roomID, bob.UserID)
			bob.JoinRoom(t, roomID, []string{})
			messageID := bob.SendEventSynced(t, roomID, b.Event{
				Type: "m.room.message",
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    "Before leave",
				},
			})
			// Bob leaves room
			bob.LeaveRoom(t, roomID)
			bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "rooms", roomID, "forget"})
			joinRes := bob.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "join", roomID})
			must.MatchResponse(t, joinRes, match.HTTPResponse{
				StatusCode: http.StatusForbidden,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_FORBIDDEN"),
				},
			})
			// Re-invite bob
			alice.InviteRoom(t, roomID, bob.UserID)
			bob.JoinRoom(t, roomID, []string{})
			queryParams := url.Values{}
			queryParams.Set("dir", "b")
			res := bob.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
			msgRes := &msgResult{}
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON:       []match.JSON{findMessageId(messageID, msgRes)},
			})
			if !msgRes.found {
				t.Errorf("did not find expected 'before leave' message")
			}
			messageID = bob.SendEventSynced(t, roomID, b.Event{
				Type: "m.room.message",
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    "After rejoin",
				},
			})
			msgRes.found = false
			res = bob.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON:       []match.JSON{findMessageId(messageID, msgRes)},
			})
			if !msgRes.found {
				t.Errorf("did not find expected 'After rejoin' message")
			}
		})

		t.Run("Can forget room we weren't an actual member", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "private_chat"})
			alice.InviteRoom(t, roomID, bob.UserID)
			// Bob rejects the invite
			bob.LeaveRoom(t, roomID)
			// Bob tries to forget about this room
			bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "rooms", roomID, "forget"})
			// Alice also leaves the room
			alice.LeaveRoom(t, roomID)
			// Alice tries to forget about this room
			alice.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "rooms", roomID, "forget"})
		})
	})
}

type msgResult struct {
	found bool
}

func findMessageId(messageID string, res *msgResult) match.JSON {
	return match.JSONArrayEach("chunk", func(r gjson.Result) error {
		if r.Get("event_id").Str == messageID {
			res.found = true
			return nil
		}
		return nil
	})
}
