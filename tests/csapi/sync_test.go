package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
)

// Observes "first bug" from https://github.com/matrix-org/dendrite/pull/1394#issuecomment-687056673
func TestCumulativeJoinLeaveJoinSync(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := bob.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	var since string

	// Get floating next_batch from before joining at all
	_, since = alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

	alice.JoinRoom(t, roomID, nil)

	// This assumes that sync does not have side-effects in servers.
	//
	// The alternative would be to sleep, but that is not acceptable here.
	sinceJoin := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

	alice.LeaveRoom(t, roomID)

	sinceLeave := alice.MustSyncUntil(t, client.SyncReq{Since: sinceJoin}, client.SyncLeftFrom(alice.UserID, roomID))

	alice.JoinRoom(t, roomID, nil)

	alice.MustSyncUntil(t, client.SyncReq{Since: sinceLeave}, client.SyncJoinedTo(alice.UserID, roomID))

	jsonRes, since := alice.MustSync(t, client.SyncReq{TimeoutMillis: "0", Since: since})
	if jsonRes.Get("rooms.leave." + client.GjsonEscape(roomID)).Exists() {
		t.Errorf("Incremental sync has joined-left-joined room showing up in leave section, this shouldnt be the case.")
	}
}

// Observes "second bug" from https://github.com/matrix-org/dendrite/pull/1394#issuecomment-687056673
func TestTentativeEventualJoiningAfterRejecting(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	var since string

	// Get floating current next_batch
	_, since = alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

	alice.InviteRoom(t, roomID, bob.UserID)

	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))

	// This rejects the invite
	bob.LeaveRoom(t, roomID)

	// Full sync
	jsonRes, since := bob.MustSync(t, client.SyncReq{TimeoutMillis: "0", FullState: true, Since: since})
	if !jsonRes.Get("rooms.leave." + client.GjsonEscape(roomID)).Exists() {
		t.Errorf("Bob just rejected an invite, it should show up under 'leave' in a full sync")
	}

	bob.JoinRoom(t, roomID, nil)

	jsonRes, since = bob.MustSync(t, client.SyncReq{TimeoutMillis: "0", FullState: true, Since: since})
	if jsonRes.Get("rooms.leave." + client.GjsonEscape(roomID)).Exists() {
		t.Errorf("Bob has rejected an invite, but then just joined the public room anyways, it should not show up under 'leave' in a full sync %s", since)
	}
}
