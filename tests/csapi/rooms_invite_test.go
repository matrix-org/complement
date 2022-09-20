package csapi_tests

import (
	"net/http"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
)

func TestRoomsInvite(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	t.Run("Parallel", func(t *testing.T) {
		// sytest: Can invite users to invite-only rooms
		t.Run("Can invite users to invite-only rooms", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})
			alice.InviteRoom(t, roomID, bob.UserID)
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			bob.JoinRoom(t, roomID, []string{})
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
		})

		// sytest: Uninvited users cannot join the room
		t.Run("Uninvited users cannot join the room", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})
			res := bob.DoFunc(t, "POST", []string{"_matrix", "client", "v3", "join", roomID})
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusForbidden,
			})
		})

		// sytest: Invited user can reject invite
		t.Run("Invited user can reject invite", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})
			alice.InviteRoom(t, roomID, bob.UserID)
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			bob.LeaveRoom(t, roomID)
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(bob.UserID, roomID))
		})

		// sytest: Invited user can reject invite for empty room
		t.Run("Invited user can reject invite for empty room", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})

			aliceSince := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
			alice.InviteRoom(t, roomID, bob.UserID)
			bobSince := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			alice.LeaveRoom(t, roomID)
			alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncLeftFrom(alice.UserID, roomID))
			bob.LeaveRoom(t, roomID)
			bobSince = bob.MustSyncUntil(t, client.SyncReq{Since: bobSince}, client.SyncLeftFrom(bob.UserID, roomID))
			// sytest: Invited user can reject local invite after originator leaves
			// Bob should not see an invite when syncing
			res, _ := bob.MustSync(t, client.SyncReq{Since: bobSince})
			// we filter on the specific roomID, since we run in parallel
			if res.Get("rooms.invite." + client.GjsonEscape(roomID)).Exists() {
				t.Fatalf("rooms.invite should not exist: %+v", res.Get("rooms.invite").Raw)
			}
		})

		// sytest: Users cannot invite themselves to a room
		t.Run("Users cannot invite themselves to a room", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})
			body := client.WithJSONBody(t, map[string]interface{}{
				"user_id": alice.UserID,
			})
			res := alice.DoFunc(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "invite"}, body)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusForbidden,
			})
		})

		// sytest: Users cannot invite a user that is already in the room
		t.Run("Users cannot invite a user that is already in the room", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})

			alice.InviteRoom(t, roomID, bob.UserID)
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			bob.JoinRoom(t, roomID, []string{})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

			body := client.WithJSONBody(t, map[string]interface{}{
				"user_id": bob.UserID,
			})
			res := alice.DoFunc(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "invite"}, body)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusForbidden,
			})
		})

		// sytest: Invited user can see room metadata
		t.Run("Invited user can see room metadata", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
				"name":   "Invites room",
			})

			alice.InviteRoom(t, roomID, bob.UserID)
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			res, _ := bob.MustSync(t, client.SyncReq{})
			verifyState(t, res, roomID, alice)
		})

		// sytest: Test that we can be reinvited to a room we created
		// This is a "multi_test" in Sytest
		t.Run("Test that we can be reinvited to a room we created", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})

			// Invite & join bob
			alice.InviteRoom(t, roomID, bob.UserID)
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			bob.JoinRoom(t, roomID, []string{})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))

			// Raise the powerlevel
			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"users": map[string]int64{
					bob.UserID: 100,
				},
			})
			alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.power_levels"}, reqBody)

			// Alice leaves the room
			alice.LeaveRoom(t, roomID)
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(alice.UserID, roomID))
			// Bob re-invites Alice
			bob.InviteRoom(t, roomID, alice.UserID)
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, roomID))
			// Alice should be able to rejoin
			alice.JoinRoom(t, roomID, []string{})
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
		})
	})
}

// verifyState checks that the fields in "wantFields" are present in invite_state.events
func verifyState(t *testing.T, res gjson.Result, roomID string, cl *client.CSAPI) {
	wantFields := map[string]string{
		"m.room.join_rules": "join_rule",
		"m.room.name":       "name",
	}

	for _, event := range res.Get("rooms.invite." + client.GjsonEscape(roomID) + ".invite_state.events").Array() {
		eventType := event.Get("type").Str
		field, ok := wantFields[eventType]
		if !ok {
			continue
		}
		eventContent := event.Get("content." + field).Str
		eventStateKey := event.Get("state_key").Str

		res := cl.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", eventType, eventStateKey})

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual(field, eventContent),
			},
		})
	}
}
