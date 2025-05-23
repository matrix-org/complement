package tests

import (
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/gomatrixserverlib"

	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
)

// This test ensures that invite rejections are correctly sent out over federation.
//
// We start with two users in a room - alice@hs1, and 'delia' on the Complement test server.
// alice sends an invite to charlie@hs2, which he rejects.
// We check that delia sees the rejection.
func TestFederationRejectInvite(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	charlie := deployment.Register(t, "hs2", helpers.RegistrationOpts{})

	// we'll awaken this Waiter when we receive a membership event for Charlie
	var waiter *helpers.Waiter

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleTransactionRequests(func(ev gomatrixserverlib.PDU) {
			sk := "<nil>"
			if ev.StateKey() != nil {
				sk = *ev.StateKey()
			}
			t.Logf("Received PDU %s/%s", ev.Type(), sk)
			if waiter != nil && ev.Type() == "m.room.member" && ev.StateKeyEquals(charlie.UserID) {
				waiter.Finish()
			}
		}, nil),
	)
	srv.UnexpectedRequestsAreErrors = false
	cancel := srv.Listen()
	defer cancel()
	delia := srv.UserID("delia")

	// Alice creates the room, and delia joins
	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	room := srv.MustJoinRoom(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), roomID, delia)

	// Alice invites Charlie; Delia should see the invite
	waiter = helpers.NewWaiter()
	alice.MustInviteRoom(t, roomID, charlie.UserID)
	waiter.Wait(t, 5*time.Second)
	room.MustHaveMembershipForUser(t, charlie.UserID, "invite")

	// Charlie rejects the invite; Delia should see the rejection.
	waiter = helpers.NewWaiter()
	charlie.MustLeaveRoom(t, roomID)
	waiter.Wait(t, 5*time.Second)
	room.MustHaveMembershipForUser(t, charlie.UserID, "leave")
}
