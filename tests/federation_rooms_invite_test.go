package tests

import (
	"net/url"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestFederationRoomsInvite(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs2", "@bob:hs2")

	roomID := alice.CreateRoom(t, map[string]interface{}{
		"preset": "private_chat",
		"name":   "Invites room",
		// invite Bob and make the room a DM, so the first test can verify m.direct flag is in the prev_content
		"invite":    []string{bob.UserID},
		"is_direct": true,
	})

	aliceSince := ""
	bobSince := ""

	t.Run("Invited user has 'is_direct' flag in prev_content after joining", func(t *testing.T) {
		bob.JoinRoom(t, roomID, []string{})
		queryParams := url.Values{}
		queryParams.Set("format", "event")
		bob.Debug = true
		bobSince = bob.MustSyncUntil(t, client.SyncReq{Since: bobSince}, client.SyncJoinedTo(bob.UserID, roomID))
		bob.Debug = false
		// now get the direct flag from the server
		res := bob.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.member", bob.UserID}, client.WithQueries(queryParams))
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("unsigned.prev_content.membership", "invite"),
				match.JSONKeyEqual("unsigned.prev_content.is_direct", true),
			},
		})
		// leave again,
		bob.LeaveRoom(t, roomID)
		aliceSince = alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncLeftFrom(bob.UserID, roomID))
	})

	// sytest: Invited user can reject invite over federation
	t.Run("Invited user can reject invite over federation", func(t *testing.T) {
		alice.InviteRoom(t, roomID, bob.UserID)
		bobSince = bob.MustSyncUntil(t, client.SyncReq{Since: bobSince}, client.SyncInvitedTo(bob.UserID, roomID))
		bob.LeaveRoom(t, roomID)
		aliceSince = alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncLeftFrom(bob.UserID, roomID))
	})

	// sytest: Invited user can reject invite over federation several times
	t.Run("Invited user can reject invite over federation several times", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			alice.InviteRoom(t, roomID, bob.UserID)
			bobSince = bob.MustSyncUntil(t, client.SyncReq{Since: bobSince}, client.SyncInvitedTo(bob.UserID, roomID))
			bob.LeaveRoom(t, roomID)
			aliceSince = alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncLeftFrom(bob.UserID, roomID))
		}
	})

	// sytest: Remote invited user can see room metadata
	t.Run("Remote invited user can see room metadata", func(t *testing.T) {
		wantFields := map[string]string{
			"m.room.join_rules": "join_rule",
			"m.room.name":       "name",
		}
		wantValues := map[string]string{
			"m.room.join_rules": "invite",
			"m.room.name":       "Invites room",
		}
		alice.InviteRoom(t, roomID, bob.UserID)
		bob.MustSyncUntil(t, client.SyncReq{Since: bobSince}, client.SyncInvitedTo(bob.UserID, roomID))
		var res gjson.Result
		res, bobSince = bob.MustSync(t, client.SyncReq{Since: bobSince})
		verifyState(t, res, wantFields, wantValues, roomID, alice)
	})

	// sytest: Invited user can reject invite over federation for empty room
	t.Run("Invited user can reject invite over federation for empty room", func(t *testing.T) {
		alice.InviteRoom(t, roomID, bob.UserID)
		bobSince = bob.MustSyncUntil(t, client.SyncReq{Since: bobSince}, client.SyncInvitedTo(bob.UserID, roomID))
		alice.LeaveRoom(t, roomID)
		alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncLeftFrom(alice.UserID, roomID))
		bob.LeaveRoom(t, roomID)
		bob.MustSyncUntil(t, client.SyncReq{Since: bobSince}, client.SyncLeftFrom(bob.UserID, roomID))
	})
}

// verifyState checks that the fields in "wantFields" are present in invite_state.events
func verifyState(t *testing.T, res gjson.Result, wantFields, wantValues map[string]string, roomID string, cl *client.CSAPI) {
	inviteEvents := res.Get("rooms.invite." + client.GjsonEscape(roomID) + ".invite_state.events")
	if !inviteEvents.Exists() {
		t.Errorf("expected invite events, but they don't exist")
	}
	for _, event := range inviteEvents.Array() {
		eventType := event.Get("type").Str
		field, ok := wantFields[eventType]
		if !ok {
			continue
		}
		wantValue := wantValues[eventType]
		eventStateKey := event.Get("state_key").Str

		res := cl.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", eventType, eventStateKey})

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual(field, wantValue),
			},
		})
	}
}
