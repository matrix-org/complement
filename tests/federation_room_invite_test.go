package tests

import (
	"fmt"
	"testing"
	"time"

	"github.com/matrix-org/gomatrixserverlib"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/federation"
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

	waiter := NewWaiter()
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleTransactionRequests(func(ev *gomatrixserverlib.Event) {
			defer waiter.Finish()
			sk := "<nil>"
			if ev.StateKey() != nil {
				sk = *ev.StateKey()
			}
			t.Logf("Received PDU %s/%s", ev.Type(), sk)
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
	alice.InviteRoom(t, roomID, charlie.UserID)
	waiter.Wait(t, 5*time.Second)
	if err := checkMembershipForUser(room, charlie.UserID, "invite"); err != nil {
		t.Errorf("Membership state for charlie after invite: %v", err)
	}

	// Charlie rejects the invite; Delia should see the rejection.
	waiter = NewWaiter()
	charlie.LeaveRoom(t, roomID)
	waiter.Wait(t, 5*time.Second)
	if err := checkMembershipForUser(room, charlie.UserID, "leave"); err != nil {
		t.Errorf("Membership state for charlie after reject: %v", err)
	}
}

func checkMembershipForUser(room *federation.ServerRoom, userID, wantMembership string) (err error) {
	state := room.CurrentState("m.room.member", userID)
	if state == nil {
		err = fmt.Errorf("no membership state for %s", userID)
		return
	}
	m, err := state.Membership()
	if err != nil {
		return
	}
	if m != wantMembership {
		err = fmt.Errorf("incorrect membership state: got %s, want %s", m, wantMembership)
		return
	}
	return
}
