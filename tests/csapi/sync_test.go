package csapi_tests

import (
	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/tidwall/gjson"
	"net/url"
	"testing"
	"time"
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
	res := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "sync"}, client.WithQueries(url.Values{
		"timeout": []string{"0"},
	}))
	body := client.ParseJSON(t, res)
	since = client.GetJSONFieldStr(t, body, "next_batch")

	alice.JoinRoom(t, roomID, nil)

	// We can't SyncUntil or else "pollute" the sync internal state, we must do as-if we're a slow client.
	// Thus, two-second delays.
	// WARNING: If you're wondering why this test is failing in a flaky fashion, and are looking at this test,
	// this may the reason why, sorry :(
	time.Sleep(2 * time.Second)

	alice.LeaveRoom(t, roomID)

	time.Sleep(2 * time.Second)

	alice.JoinRoom(t, roomID, nil)

	// Final sleep to propagate server-internal state
	time.Sleep(2 * time.Second)

	res = alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "sync"}, client.WithQueries(url.Values{
		"timeout": []string{"0"},
		"since":   []string{since},
	}))
	body = client.ParseJSON(t, res)
	jsonRes := gjson.GetBytes(body, "rooms.leave."+client.GjsonEscape(roomID))
	if jsonRes.Exists() {
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
	res := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "sync"}, client.WithQueries(url.Values{
		"timeout": []string{"0"},
	}))
	body := client.ParseJSON(t, res)
	since = client.GetJSONFieldStr(t, body, "next_batch")

	alice.InviteRoom(t, roomID, bob.UserID)

	bob.SyncUntilInvitedTo(t, roomID)

	// This rejects the invite
	bob.LeaveRoom(t, roomID)

	// Full sync
	res = bob.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "sync"}, client.WithQueries(url.Values{
		"timeout":    []string{"0"},
		"full_state": []string{"true"},
		"since":      []string{since},
	}))
	body = client.ParseJSON(t, res)
	jsonRes := gjson.GetBytes(body, "rooms.leave."+client.GjsonEscape(roomID))
	if !jsonRes.Exists() {
		t.Errorf("Bob just rejected an invite, it should show up under 'leave' in a full sync")
	}
	since = client.GetJSONFieldStr(t, body, "next_batch")

	bob.JoinRoom(t, roomID, nil)

	res = bob.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "sync"}, client.WithQueries(url.Values{
		"timeout":    []string{"0"},
		"full_state": []string{"true"},
		"since":      []string{since},
	}))
	body = client.ParseJSON(t, res)
	jsonRes = gjson.GetBytes(body, "rooms.leave."+client.GjsonEscape(roomID))
	if jsonRes.Exists() {
		t.Errorf("Bob has rejected an invite, but then just joined the public room anyways, it should not show up under 'leave' in a full sync %s", since)
	}
}
