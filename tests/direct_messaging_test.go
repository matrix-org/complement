package tests

import (
	"context"
	"fmt"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/federation"

	"github.com/matrix-org/gomatrixserverlib"
	"github.com/tidwall/gjson"
)

func TestIsDirectFlagLocal(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")
	roomID := alice.CreateRoom(t, map[string]interface{}{
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
	dmInviteEvent := srv.MustCreateEvent(t, room, b.Event{
		Type:     "m.room.member",
		StateKey: &alice.UserID,
		Sender:   bob,
		Content: map[string]interface{}{
			"membership": "invite",
			"is_direct":  true,
		},
	}).Headered(roomVer)
	inviteReq, err := gomatrixserverlib.NewInviteV2Request(dmInviteEvent, []gomatrixserverlib.InviteV2StrippedState{})
	if err != nil {
		t.Fatalf("failed to make invite request: %s", err)
	}
	_, since := alice.MustSync(t, client.SyncReq{})
	_, err = srv.FederationClient(deployment).SendInviteV2(context.Background(), "hs1", inviteReq)
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
