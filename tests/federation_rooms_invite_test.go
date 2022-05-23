package tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
)

func TestFederationRoomsInvite(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs2", "@bob:hs2")

	t.Run("Parallel", func(t *testing.T) {
		// sytest: Invited user can reject invite over federation
		t.Run("Invited user can reject invite over federation", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})
			alice.InviteRoom(t, roomID, bob.UserID)
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			bob.LeaveRoom(t, roomID)
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(bob.UserID, roomID))
		})

		// sytest: Invited user can reject invite over federation several times
		t.Run("Invited user can reject invite over federation several times", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})
			for i := 0; i < 3; i++ {
				alice.InviteRoom(t, roomID, bob.UserID)
				bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
				bob.LeaveRoom(t, roomID)
				alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(bob.UserID, roomID))
			}
		})

		// sytest: Invited user can reject invite over federation for empty room
		t.Run("Invited user can reject invite over federation for empty room", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})
			aliceSince := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
			alice.InviteRoom(t, roomID, bob.UserID)
			charlieSince := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			alice.LeaveRoom(t, roomID)
			alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncLeftFrom(alice.UserID, roomID))
			bob.LeaveRoom(t, roomID)
			bob.MustSyncUntil(t, client.SyncReq{Since: charlieSince}, client.SyncLeftFrom(bob.UserID, roomID))
		})

		// sytest: Remote invited user can see room metadata
		t.Run("Remote invited user can see room metadata", func(t *testing.T) {
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
