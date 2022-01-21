package tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
)

// Regression test for https://github.com/matrix-org/synapse/issues/1563
// Create a federation room. Bob bans Alice. Bob unbans Alice. Bob invites Alice (unbanning her). Ensure the invite is
// received and can be accepted.
func TestUnbanViaInvite(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs2", "@bob:hs2")

	roomID := bob.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})
	alice.JoinRoom(t, roomID, []string{"hs2"})

	// Ban Alice
	bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "rooms", roomID, "ban"}, client.WithJSONBody(t, map[string]interface{}{
		"user_id": alice.UserID,
	}))
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(alice.UserID, roomID))

	// Unban Alice
	bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "rooms", roomID, "unban"}, client.WithJSONBody(t, map[string]interface{}{
		"user_id": alice.UserID,
	}))
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(alice.UserID, roomID))

	// Re-invite Alice
	bob.InviteRoom(t, roomID, alice.UserID)
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, roomID))
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, roomID))

	// Alice accepts (this is what previously failed in the issue)
	alice.JoinRoom(t, roomID, []string{"hs2"})
}
