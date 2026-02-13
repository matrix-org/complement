package tests

import (
	"maps"
	"slices"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/must"
	"github.com/tidwall/gjson"
)

func TestSync(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "alice"})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "bob"})

	t.Run("parallel", func(t *testing.T) {
		// When lazy-loading room members is enabled, the `state_after` in an initial sync
		// request should include membership from every `sender` in the `timeline`
		//
		// We're specifically testing the scenario where a new "DM" is created and the other person
		// joins without speaking yet.
		t.Run("Initial sync with lazy-loading room members -> `state_after` includes all members from timeline", func(t *testing.T) {
				t.Parallel()

				// Alice creates a room
				roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "trusted_private_chat"})
				alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

				// Alice invites Bob
				alice.MustInviteRoom(t, roomID, bob.UserID)

				// Bob must get the invite
				bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))

				// Bob joins the room
				bob.MustJoinRoom(t, roomID, nil)

				// Make double sure that bob is joined to the room
				alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

				// Now, Alice makes an initial sync request with lazy-loading members enabled
				//
				// The spec says `lazy_load_members` is valid field for both `timeline` and
				// `state` but as far as I can tell, only makes sense for `state` and that's
				// what Synapse keys off of.
				aliceSyncFilter := `{
				    "room": {
					    "timeline": { "limit": 20 },
					    "state": { "lazy_load_members": true }
					}
				}`
				res, _ := alice.MustSync(t, client.SyncReq{UseStateAfter: true, Filter: aliceSyncFilter})
				joinedRoomRes := res.Get("rooms.join." + client.GjsonEscape(roomID))
				if !joinedRoomRes.Exists() {
					t.Fatalf("Unable to find roomID=%s in the join part of the sync response: %s", roomID, res)
				}

				// Collect the senders of all the time timeline events.
				roomTimelineRes := joinedRoomRes.Get("timeline.events");
				if !roomTimelineRes.IsArray() {
					t.Fatalf("Timeline events is not an array (found %s) %s", roomTimelineRes.Type.String(), res)
				}
				sendersFromTimeline := make(map[string]struct{}, 0)
				for _, event := range roomTimelineRes.Array() {
					sendersFromTimeline[event.Get("sender").Str] = struct{}{}
				}
				// We expect to see timeline events from alice and bob
				must.ContainSubset(t,
					slices.Collect(maps.Keys(sendersFromTimeline)),
					[]string{ alice.UserID, bob.UserID },
				)

				// Collect the `m.room.membership` from `state_after`
				//
				// Try looking up the stable variant `state_after` first, then fallback to the
				// unstable version
				roomStateAfterResStable := joinedRoomRes.Get("state_after.events");
				roomStateAfterResUnstable := joinedRoomRes.Get("org\\.matrix\\.msc4222\\.state_after.events");
				var roomStateAfterRes gjson.Result
				if roomStateAfterResStable.Exists() {
					roomStateAfterRes = roomStateAfterResStable
				} else if roomStateAfterResUnstable.Exists() {
					roomStateAfterRes = roomStateAfterResUnstable
				}
				// Sanity check syntax
				if !roomStateAfterRes.IsArray() {
					t.Fatalf("state_after events is not an array (found %s) %s", roomStateAfterRes.Type.String(), res)
				}
				membershipFromState := make(map[string]struct{}, 0)
				for _, event := range roomStateAfterRes.Array() {
					if event.Get("type").Str == "m.room.member" {
						membershipFromState[event.Get("sender").Str] = struct{}{}
					}
				}
				// We should see membership state from every `sender` in the `timeline` (alice
				// and bob).
				must.ContainSubset(t,
					slices.Collect(maps.Keys(membershipFromState)),
					slices.Collect(maps.Keys(sendersFromTimeline)),
				)

		})
	})
}
