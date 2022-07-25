package tests

import (
	"testing"
	"time"

	"github.com/matrix-org/gomatrixserverlib"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/complement/internal/waiter"
)

// This test ensures that invite rejections are correctly sent out over federation.
//
// We start with two users in a room - alice@hs1, and 'delia' on the Complement test server.
// alice sends an invite to charlie@hs2, which he rejects.
// We check that delia sees the rejection.
//
func TestFederationRejectInvite(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationTwoLocalOneRemote)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	charlie := deployment.Client(t, "hs2", "@charlie:hs2")

	// we'll awaken this Waiter when we receive a membership event for Charlie
	var w *waiter.Waiter

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleTransactionRequests(func(ev *gomatrixserverlib.Event) {
			sk := "<nil>"
			if ev.StateKey() != nil {
				sk = *ev.StateKey()
			}
			t.Logf("Received PDU %s/%s", ev.Type(), sk)
			if w != nil && ev.Type() == "m.room.member" && ev.StateKeyEquals(charlie.UserID) {
				w.Finish()
			}
		}, nil),
	)
	srv.UnexpectedRequestsAreErrors = false
	cancel := srv.Listen()
	defer cancel()
	delia := srv.UserID("delia")

	// Alice creates the room, and delia joins
	roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	room := srv.MustJoinRoom(t, deployment, "hs1", roomID, delia)

	// Alice invites Charlie; Delia should see the invite
	w = waiter.New()
	alice.InviteRoom(t, roomID, charlie.UserID)
	w.Wait(t, 5*time.Second)
	room.MustHaveMembershipForUser(t, charlie.UserID, "invite")

	// Charlie rejects the invite; Delia should see the rejection.
	w = waiter.New()
	charlie.LeaveRoom(t, roomID)
	w.Wait(t, 5*time.Second)
	room.MustHaveMembershipForUser(t, charlie.UserID, "leave")
}
