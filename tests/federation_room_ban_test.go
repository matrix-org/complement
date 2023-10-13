package tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
)

// Regression test for https://github.com/matrix-org/synapse/issues/1563
// Create a federation room. Bob bans Alice. Bob unbans Alice. Bob invites Alice (unbanning her). Ensure the invite is
// received and can be accepted.
func TestUnbanViaInvite(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})

	roomID := bob.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})
	alice.MustJoinRoom(t, roomID, []string{"hs2"})

	// Ban Alice
	bob.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "ban"}, client.WithJSONBody(t, map[string]interface{}{
		"user_id": alice.UserID,
	}))
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(alice.UserID, roomID))

	// Unban Alice
	bob.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "unban"}, client.WithJSONBody(t, map[string]interface{}{
		"user_id": alice.UserID,
	}))
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(alice.UserID, roomID))

	// Re-invite Alice
	bob.MustInviteRoom(t, roomID, alice.UserID)
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, roomID))
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, roomID))

	// Alice accepts (this is what previously failed in the issue)
	alice.MustJoinRoom(t, roomID, []string{"hs2"})
}
