package csapi_tests

import (
	"net/http"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/tidwall/gjson"
)

func TestRoomsInvite(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	t.Run("Parallel", func(t *testing.T) {
		// sytest: Can invite users to invite-only rooms
		t.Run("Can invite users to invite-only rooms", func(t *testing.T) {
			t.Parallel()
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})
			alice.MustInviteRoom(t, roomID, bob.UserID)
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			bob.MustJoinRoom(t, roomID, []spec.ServerName{})
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
		})

		// sytest: Uninvited users cannot join the room
		t.Run("Uninvited users cannot join the room", func(t *testing.T) {
			t.Parallel()
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})
			res := bob.JoinRoom(t, roomID, nil)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusForbidden,
			})
		})

		// sytest: Invited user can reject invite
		t.Run("Invited user can reject invite", func(t *testing.T) {
			t.Parallel()
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})
			alice.MustInviteRoom(t, roomID, bob.UserID)
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			bob.MustLeaveRoom(t, roomID)
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(bob.UserID, roomID))
		})

		// sytest: Invited user can reject invite for empty room
		t.Run("Invited user can reject invite for empty room", func(t *testing.T) {
			t.Parallel()
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})

			aliceSince := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
			alice.MustInviteRoom(t, roomID, bob.UserID)
			bobSince := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			alice.MustLeaveRoom(t, roomID)
			alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncLeftFrom(alice.UserID, roomID))
			bob.MustLeaveRoom(t, roomID)
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
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})
			res := alice.InviteRoom(t, roomID, alice.UserID)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusForbidden,
			})
		})

		// sytest: Users cannot invite a user that is already in the room
		t.Run("Users cannot invite a user that is already in the room", func(t *testing.T) {
			t.Parallel()
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})

			alice.MustInviteRoom(t, roomID, bob.UserID)
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			bob.MustJoinRoom(t, roomID, []spec.ServerName{})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

			res := alice.InviteRoom(t, roomID, bob.UserID)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusForbidden,
			})
		})

		// sytest: Invited user can see room metadata
		t.Run("Invited user can see room metadata", func(t *testing.T) {
			t.Parallel()
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
				"name":   "Invites room",
			})

			alice.MustInviteRoom(t, roomID, bob.UserID)
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			res, _ := bob.MustSync(t, client.SyncReq{})
			verifyState(t, res, roomID, alice)
		})

		// sytest: Test that we can be reinvited to a room we created
		// This is a "multi_test" in Sytest
		t.Run("Test that we can be reinvited to a room we created", func(t *testing.T) {
			t.Parallel()
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})

			// Invite & join bob
			alice.MustInviteRoom(t, roomID, bob.UserID)
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			bob.MustJoinRoom(t, roomID, []spec.ServerName{})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))

			// Raise the powerlevel
			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"users": map[string]int64{
					bob.UserID: 100,
				},
			})
			alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.power_levels"}, reqBody)

			// Alice leaves the room
			alice.MustLeaveRoom(t, roomID)
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(alice.UserID, roomID))
			// Bob re-invites Alice
			bob.MustInviteRoom(t, roomID, alice.UserID)
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, roomID))
			// Alice should be able to rejoin
			alice.MustJoinRoom(t, roomID, []spec.ServerName{})
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

		res := cl.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", eventType, eventStateKey})

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual(field, eventContent),
			},
		})
	}
}
