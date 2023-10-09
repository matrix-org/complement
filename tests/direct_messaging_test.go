package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/matrix-org/gomatrixserverlib/spec"

	"github.com/matrix-org/gomatrixserverlib"
	"github.com/tidwall/gjson"
)

// Test that a client can write `m.direct` account data and get told about updates to that event.
// Requires a functioning account data implementation.
func TestWriteMDirectAccountData(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer func() {
		// additional logging to debug https://github.com/matrix-org/synapse/issues/13334
		t.Logf("%s: TestWriteMDirectAccountData complete: destroying HS deployment", time.Now())
		deployment.Destroy(t)
	}()

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"invite":    []string{bob.UserID},
		"is_direct": true,
	})
	alice.MustSetGlobalAccountData(t, "m.direct", map[string]interface{}{
		bob.UserID: []string{roomID},
	})

	checkAccountData := func(r gjson.Result) bool {
		if r.Get("type").Str != "m.direct" {
			return false
		}
		must.MatchGJSON(t, r, match.JSONKeyEqual("content."+client.GjsonEscape(bob.UserID), []string{roomID}))
		return true
	}
	t.Logf("%s: global account data set; syncing until it arrives", time.Now()) // synapse#13334
	since := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncGlobalAccountDataHas(checkAccountData))
	// now update the DM room and test that incremental syncing also pushes new account data
	roomID = alice.MustCreateRoom(t, map[string]interface{}{
		"invite":    []string{bob.UserID},
		"is_direct": true,
	})
	alice.MustSetGlobalAccountData(t, "m.direct", map[string]interface{}{
		bob.UserID: []string{roomID},
	})
	alice.MustSyncUntil(t, client.SyncReq{Since: since}, client.SyncGlobalAccountDataHas(checkAccountData))

	// check that manually GETing the account data also works with the new updated value
	must.MatchResponse(t, alice.MustGetGlobalAccountData(t, "m.direct"), match.HTTPResponse{
		StatusCode: 200,
		JSON: []match.JSON{
			match.JSONKeyEqual(client.GjsonEscape(bob.UserID), []interface{}{roomID}),
		},
	})
}

// Test that the `is_direct` flag on m.room.member invites propagate to the target user. Both users
// are on the same homeserver.
func TestIsDirectFlagLocal(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"invite":    []string{bob.UserID},
		"is_direct": true,
	})
	bob.MustSyncUntil(t, client.SyncReq{}, func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		inviteStateEvents := topLevelSyncJSON.Get("rooms.invite." + client.GjsonEscape(roomID) + ".invite_state.events").Array()
		for _, ev := range inviteStateEvents {
			if ev.Get("type").Str == "m.room.member" &&
				ev.Get("state_key").Str == bob.UserID &&
				ev.Get("content.membership").Str == "invite" {
				t.Logf("Received invite: %v", ev.Raw)
				if !ev.Get("content.is_direct").Exists() {
					t.Logf("missing is_direct flag")
					return fmt.Errorf("invite exists but missing is_direct")
				}
				if ev.Get("content.is_direct").Bool() {
					return nil
				}
				return fmt.Errorf("is_direct is not true")
			}
		}
		return fmt.Errorf("missing invite event")
	})
}

// Test that the `is_direct` flag on m.room.member invites propagate to the target user. Users
// are on different homeservers.
func TestIsDirectFlagFederation(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(nil, nil),
	)
	srv.UnexpectedRequestsAreErrors = false // we expect to be pushed events
	cancel := srv.Listen()
	defer cancel()
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	roomVer := alice.GetDefaultRoomVersion(t)

	bob := srv.UserID("bob")
	room := srv.MustMakeRoom(t, roomVer, federation.InitialRoomEvents(roomVer, bob))
	dmInviteEvent := srv.MustCreateEvent(t, room, federation.Event{
		Type:     "m.room.member",
		StateKey: &alice.UserID,
		Sender:   bob,
		Content: map[string]interface{}{
			"membership": "invite",
			"is_direct":  true,
		},
	})
	inviteReq, err := fclient.NewInviteV2Request(dmInviteEvent, []gomatrixserverlib.InviteStrippedState{})
	if err != nil {
		t.Fatalf("failed to make invite request: %s", err)
	}
	_, since := alice.MustSync(t, client.SyncReq{})
	_, err = srv.FederationClient(deployment).SendInviteV2(context.Background(), spec.ServerName(srv.ServerName()), "hs1", inviteReq)
	if err != nil {
		t.Fatalf("failed to send invite v2: %s", err)
	}

	alice.MustSyncUntil(t, client.SyncReq{Since: since}, func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		inviteStateEvents := topLevelSyncJSON.Get("rooms.invite." + client.GjsonEscape(room.RoomID) + ".invite_state.events").Array()
		for _, ev := range inviteStateEvents {
			if ev.Get("type").Str == "m.room.member" &&
				ev.Get("state_key").Str == alice.UserID &&
				ev.Get("content.membership").Str == "invite" {
				t.Logf("Received invite: %v", ev.Raw)
				if !ev.Get("content.is_direct").Exists() {
					t.Logf("missing is_direct flag")
					return fmt.Errorf("invite exists but missing is_direct")
				}
				if ev.Get("content.is_direct").Bool() {
					return nil
				}
				return fmt.Errorf("is_direct is not true")
			}
		}
		return fmt.Errorf("missing invite event")
	})
}
