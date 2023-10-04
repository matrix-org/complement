package csapi_tests

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/b"
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
			res := alice.Do(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "forget"}, client.WithJSONBody(t, struct{}{}))
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
			alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "forget"}, client.WithJSONBody(t, struct{}{}))
			res := alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"})
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusForbidden,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_FORBIDDEN"),
				},
			})
		})
		// sytest: Forgetting room does not show up in v2 /sync
		t.Run("Forgetting room does not show up in v2 initial /sync", func(t *testing.T) {
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
			alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "forget"}, client.WithJSONBody(t, struct{}{}))
			bob.SendEventSynced(t, roomID, b.Event{
				Type: "m.room.message",
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    "Hello world!",
				},
			})
			includeLeaveFilter, _ := json.Marshal(map[string]interface{}{
				"room": map[string]interface{}{
					"include_leave": true,
				},
			})
			result, _ := alice.MustSync(t, client.SyncReq{Filter: string(includeLeaveFilter)})
			if result.Get("rooms.archived." + client.GjsonEscape(roomID)).Exists() {
				t.Errorf("Did not expect room %s in archived", roomID)
			}
			if result.Get("rooms.join." + client.GjsonEscape(roomID)).Exists() {
				t.Errorf("Did not expect room %s in joined", roomID)
			}
			if result.Get("rooms.invite." + client.GjsonEscape(roomID)).Exists() {
				t.Errorf("Did not expect room %s in invited", roomID)
			}
			if result.Get("rooms.leave." + client.GjsonEscape(roomID)).Exists() {
				t.Errorf("Did not expect room %s in left", roomID)
			}
		})
		t.Run("Leave for forgotten room shows up in v2 incremental /sync", func(t *testing.T) {
			// Note that this test runs counter to the wording of the spec. At the time of writing,
			// the spec says that forgotten rooms should not show up in any /sync responses, but
			// that would make it impossible for other devices to determine that a room has been
			// left if it is forgotten quickly. This is arguably a bug in the spec.
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
			tokenBeforeLeave := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
			alice.LeaveRoom(t, roomID)
			// Ensure Alice left the room
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(alice.UserID, roomID))
			alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "forget"}, client.WithJSONBody(t, struct{}{}))
			bob.SendEventSynced(t, roomID, b.Event{
				Type: "m.room.message",
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    "Hello world!",
				},
			})
			// The leave for the room is expected to show up in the next incremental /sync.
			includeLeaveFilter, _ := json.Marshal(map[string]interface{}{
				"room": map[string]interface{}{
					"include_leave": true,
				},
			})
			result, _ := alice.MustSync(t, client.SyncReq{
				Since:  tokenBeforeLeave,
				Filter: string(includeLeaveFilter),
			})
			if !result.Get("rooms.leave." + client.GjsonEscape(roomID)).Exists() {
				t.Errorf("Did not see room %s in left", roomID)
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
			alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "kick"},
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
			bob.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "forget"}, client.WithJSONBody(t, struct{}{}))
			// Try to re-join
			joinRes := bob.Do(t, "POST", []string{"_matrix", "client", "v3", "join", roomID}, client.WithJSONBody(t, struct{}{}))
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
			res := bob.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
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
			res = bob.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
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
			bob.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "forget"}, client.WithJSONBody(t, struct{}{}))
			// Alice also leaves the room
			alice.LeaveRoom(t, roomID)
			// Alice tries to forget about this room
			alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "forget"}, client.WithJSONBody(t, struct{}{}))
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
