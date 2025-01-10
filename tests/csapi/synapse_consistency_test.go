package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
)

func TestSynapseConsistency(t *testing.T) {
	numHomeservers := 2
	deployment := complement.Deploy(t, numHomeservers)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "alice"})

	charlie1 := deployment.Register(t, "hs2", helpers.RegistrationOpts{LocalpartSuffix: "charlie1"})
	charlie2 := deployment.Register(t, "hs2", helpers.RegistrationOpts{LocalpartSuffix: "charlie2"})

	t.Run("test1", func(t *testing.T) {
		// Create a room on hs1
		roomID := alice.MustCreateRoom(t, map[string]interface{}{
			"preset": "private_chat",
		})

		// Invite multiple users from hs2
		alice.MustInviteRoom(t, roomID, charlie1.UserID)
		charlie1.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(charlie1.UserID, roomID))
		charlie1.MustJoinRoom(t, roomID, []string{"hs1"})

		alice.MustInviteRoom(t, roomID, charlie2.UserID)
		charlie2.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(charlie2.UserID, roomID))
		charlie2.MustJoinRoom(t, roomID, []string{"hs1"})
	})
}
