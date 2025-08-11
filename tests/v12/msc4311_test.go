package tests

import (
	"fmt"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
	"github.com/matrix-org/gomatrixserverlib/spec"
)

func TestMSC4311FullCreateEventOnStrippedState(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // does not implement it yet
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "alice"})
	local := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "local"})
	remote := deployment.Register(t, "hs2", helpers.RegistrationOpts{LocalpartSuffix: "remote"})
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"room_version": roomVersion12,
		"preset":       "public_chat",
	})
	for _, target := range []*client.CSAPI{local, remote} {
		t.Logf("checking %s", target.UserID)
		alice.MustInviteRoom(t, roomID, target.UserID)
		resp, _ := target.MustSync(t, client.SyncReq{})
		inviteState := resp.Get(
			fmt.Sprintf("rooms.invite.%s.invite_state.events", client.GjsonEscape(roomID)),
		)
		must.NotEqual(t, len(inviteState.Array()), 0, "no events in invite_state")
		// find the create event
		found := false
		for _, ev := range inviteState.Array() {
			if ev.Get("type").Str == spec.MRoomCreate {
				found = true
				// we should have extra fields
				must.MatchGJSON(t, ev,
					match.JSONKeyPresent("origin_server_ts"),
				)
			}
		}
		if !found {
			ct.Errorf(t, "failed to find create event in invite_state")
		}
	}

}
