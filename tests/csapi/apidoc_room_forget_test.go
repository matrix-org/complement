package csapi_tests

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

// These tests ensure that forgetting about rooms works as intended
func TestRoomForget(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")
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
			// Ensure Alice left the room
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(alice.UserID, roomID))
			alice.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "rooms", roomID, "forget"})
			bob.SendEventSynced(t, roomID, b.Event{
				Type: "m.room.message",
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    "Hello world!",
				},
			})
			result, _ := alice.MustSync(t, client.SyncReq{})
			if result.Get("rooms.archived." + client.GjsonEscape(roomID)).Exists() {
				t.Errorf("Did not expect room %s in archived", roomID)
			}
			if result.Get("rooms.join." + client.GjsonEscape(roomID)).Exists() {
				t.Errorf("Did not expect room %s in joined", roomID)
			}
			if result.Get("rooms.invite." + client.GjsonEscape(roomID)).Exists() {
				t.Errorf("Did not expect room %s in invited", roomID)
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
			alice.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "rooms", roomID, "kick"},
				client.WithJSONBody(t, map[string]interface{}{
					"user_id": bob.UserID,
				}),
			)
			// Ensure Bob was really kicked
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(bob.UserID, roomID))
			result, _ := bob.MustSync(t, client.SyncReq{})
			if result.Get("rooms.archived." + client.GjsonEscape(roomID)).Exists() {
				t.Errorf("Did not expect room %s in archived", roomID)
			}
			if result.Get("rooms.join." + client.GjsonEscape(roomID)).Exists() {
				t.Errorf("Did not expect room %s in joined", roomID)
			}
			if result.Get("rooms.invite." + client.GjsonEscape(roomID)).Exists() {
				t.Errorf("Did not expect room %s in invited", roomID)
			}
		})

		// sytest: Can re-join room if re-invited
		t.Run("Can re-join room if re-invited", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "private_chat"})
			// Invite Bob
			alice.InviteRoom(t, roomID, bob.UserID)
			// Update join_rules
			alice.SendEventSynced(t, roomID, b.Event{
				Type: "m.room.join_rules",
				Content: map[string]interface{}{
					"join_rule": "invite",
				},
			})
			// Bob joins room
			bob.JoinRoom(t, roomID, []string{})
			messageID := alice.SendEventSynced(t, roomID, b.Event{
				Type: "m.room.message",
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    "Before leave",
				},
			})
			// Bob leaves and forgets room
			bob.LeaveRoom(t, roomID)
			// Ensure Bob has really left the room
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(bob.UserID, roomID))
			bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "rooms", roomID, "forget"})
			// Try to re-join
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
			// Query messages
			queryParams := url.Values{}
			queryParams.Set("dir", "b")
			queryParams.Set("limit", "100")
			// Check if we can see Bobs previous message
			res := bob.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
			msgRes := &msgResult{}
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON:       []match.JSON{findMessageId(messageID, msgRes)},
			})
			if !msgRes.found {
				t.Errorf("did not find expected 'before leave' message")
			}
			// Send new message after rejoining
			messageID = alice.SendEventSynced(t, roomID, b.Event{
				Type: "m.room.message",
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    "After rejoin",
				},
			})
			msgRes.found = false
			// We should now be able to see the new message
			queryParams.Set("limit", "1")
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
