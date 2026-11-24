//go:build !venator_blacklist

// Venator: does not yet implement federation

package tests

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
)

// An invite received over federation to a room the invitee's server is not in has no
// context on that server: `/context` on it is a 404, while `/event` still serves it.
func TestContextOnRemoteInvite(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})

	roomID := bob.MustCreateRoom(t, map[string]interface{}{"preset": "private_chat"})
	bob.MustInviteRoom(t, roomID, alice.UserID)
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, roomID))

	// The stripped state in the invitee's /sync carries no event ID.
	var inviteEventID string
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
		if ev.Get("type").Str != "m.room.member" || ev.Get("state_key").Str != alice.UserID {
			return false
		}
		inviteEventID = ev.Get("event_id").Str
		return ev.Get("content.membership").Str == "invite"
	}))

	t.Run("Context of the invite is not found", func(t *testing.T) {
		res := alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "context", inviteEventID})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_NOT_FOUND"),
			},
		})
	})

	t.Run("Invite event can be fetched", func(t *testing.T) {
		runtime.SkipIf(t, runtime.Dendrite) // returns 404 for an invite received over federation
		res := alice.GetEvent(t, roomID, inviteEventID)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyEqual("event_id", inviteEventID),
				match.JSONKeyEqual("type", "m.room.member"),
				match.JSONKeyEqual("state_key", alice.UserID),
				match.JSONKeyEqual("content.membership", "invite"),
			},
		})
	})
}
