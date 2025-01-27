// These tests currently fail on Dendrite, due to Dendrite bugs.
//go:build !dendrite_blacklist
// +build !dendrite_blacklist

package tests

import (
	"encoding/json"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

func TestFederationRoomsInvite(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "alice"})
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{LocalpartSuffix: "bob"})
	bob2 := deployment.Register(t, "hs2", helpers.RegistrationOpts{LocalpartSuffix: "bob2"})

	includeLeaveSyncFilterBytes, err := json.Marshal(map[string]interface{}{
		"room": map[string]interface{}{
			"include_leave": true,
		},
	})
	must.NotError(t, "failed to marshal include_leave filter", err)
	includeLeaveSyncFilter := string(includeLeaveSyncFilterBytes)

	t.Run("Parallel", func(t *testing.T) {
		// sytest: Invited user can reject invite over federation
		t.Run("Invited user can reject invite over federation", func(t *testing.T) {
			t.Parallel()
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
				"invite": []string{bob.UserID},
			})
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			bob.MustLeaveRoom(t, roomID)
			alice.MustSyncUntil(t, client.SyncReq{Filter: includeLeaveSyncFilter}, client.SyncLeftFrom(bob.UserID, roomID))
		})

		// sytest: Invited user can reject invite over federation several times
		t.Run("Invited user can reject invite over federation several times", func(t *testing.T) {
			t.Parallel()
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})
			for i := 0; i < 3; i++ {
				alice.MustInviteRoom(t, roomID, bob.UserID)
				bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
				bob.MustLeaveRoom(t, roomID)
				alice.MustSyncUntil(t, client.SyncReq{Filter: includeLeaveSyncFilter}, client.SyncLeftFrom(bob.UserID, roomID))
			}
		})

		// sytest: Invited user can reject invite over federation for empty room
		t.Run("Invited user can reject invite over federation for empty room", func(t *testing.T) {
			t.Parallel()
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
				"invite": []string{bob.UserID},
			})
			aliceSince := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
			bobSince := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			alice.MustLeaveRoom(t, roomID)
			alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince, Filter: includeLeaveSyncFilter}, client.SyncLeftFrom(alice.UserID, roomID))
			bob.MustLeaveRoom(t, roomID)
			bob.MustSyncUntil(t, client.SyncReq{Since: bobSince, Filter: includeLeaveSyncFilter}, client.SyncLeftFrom(bob.UserID, roomID))
		})

		// sytest: Remote invited user can see room metadata
		t.Run("Remote invited user can see room metadata", func(t *testing.T) {
			t.Parallel()
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
				"name":   "Invites room",
				"invite": []string{bob.UserID},
			})

			wantFields := map[string]string{
				"m.room.join_rules": "join_rule",
				"m.room.name":       "name",
			}
			wantValues := map[string]string{
				"m.room.join_rules": "invite",
				"m.room.name":       "Invites room",
			}

			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			res, _ := bob.MustSync(t, client.SyncReq{})
			verifyState(t, res, wantFields, wantValues, roomID, alice)
		})

		t.Run("Remote invited user can join the room when homeserver is already participating in the room", func(t *testing.T) {
			t.Parallel()
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

			// bob1 is invited and can join the room (hs2 is now participating of the room)
			alice.MustInviteRoom(t, roomID, bob.UserID)
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			bob.MustJoinRoom(t, roomID, []string{"hs1"})
			// Make sure alice can see bob in the room
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

			// bob2 is invited and can also join the room
			alice.MustInviteRoom(t, roomID, bob2.UserID)
			bob2.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob2.UserID, roomID))
			bob2.MustJoinRoom(t, roomID, []string{"hs1"})
			// Make sure alice can see bob2 in the room
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob2.UserID, roomID))
		})

		t.Run("Remote invited user can reject invite when homeserver is already participating in the room", func(t *testing.T) {
			t.Parallel()
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

			// bob1 is invited and can join the room (hs2 is now participating of the room)
			alice.MustInviteRoom(t, roomID, bob.UserID)
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
			bob.MustJoinRoom(t, roomID, []string{"hs1"})
			// Make sure alice can see bob in the room
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

			// bob2 is invited and can reject the invite (leave the room)
			alice.MustInviteRoom(t, roomID, bob2.UserID)
			bob2.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob2.UserID, roomID))
			bob2.MustLeaveRoom(t, roomID)
			// Make sure alice can see bob2 left the room
			alice.MustSyncUntil(t, client.SyncReq{Filter: includeLeaveSyncFilter}, client.SyncLeftFrom(bob2.UserID, roomID))
		})

		t.Run("Invited user has 'is_direct' flag in prev_content after joining", func(t *testing.T) {
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
				"name":   "Invites room",
				// invite Bob and make the room a DM, so we can verify m.direct flag is in the prev_content after joining
				"invite":    []string{bob.UserID},
				"is_direct": true,
			})
			bob.MustJoinRoom(t, roomID, []string{})
			bob.MustSyncUntil(t, client.SyncReq{},
				client.SyncTimelineHas(roomID, func(result gjson.Result) bool {
					// We expect a membership event ..
					if result.Get("type").Str != spec.MRoomMember {
						return false
					}
					// .. for Bob
					if result.Get("state_key").Str != bob.UserID {
						return false
					}
					// Check that we've got tbe expected is_idrect flag
					return result.Get("unsigned.prev_content.membership").Str == "invite" &&
						result.Get("unsigned.prev_content.is_direct").Bool() == true &&
						result.Get("unsigned.prev_sender").Str == alice.UserID
				}),
			)
		})
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

		res := cl.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", eventType, eventStateKey})

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual(field, wantValue),
			},
		})
	}
}
