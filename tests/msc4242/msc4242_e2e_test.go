package tests

// The tests in this file all have 2 Synapses and we drive behaviour end-to-end.
// This ensures things work, but means we don't know how they work. For API tests,
// see the other file in this directory.

import (
	"fmt"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/tidwall/gjson"
)

// Test that you can join and send messages in MSC4242 rooms.
func TestMSC4242FederationSimple(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"room_version": roomVersion,
		"preset":       "public_chat",
	})
	// ensure we are verifying current state by walking the state dag by creating no-op state dag changes.
	// The number of changes is unimportant, what's important is that we are lengthening the auth chain
	// for alice, thus the 'current state' is alice's 5th display name change, and the server must
	// verify this by walking the state DAG.
	changeDisplayName(t, alice, roomID, "alice", 5)
	bob.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})
	eventID := bob.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "I work over federation!",
		},
	})
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, eventID))
}

// Test out-of-band invites end-to-end: inviting a user whose server is not in the room. The invited
// server cannot fill in the state DAG for the invite event, either because it has never been in the
// room, or because it was in the room previously and hence has a stale state DAG. The invite is then
// either refused by the invited user or rescinded by the inviter.
func TestMSC4242OutOfBandInvites(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)
	hs1 := deployment.GetFullyQualifiedHomeserverName(t, "hs1")

	testCases := []struct {
		name string
		// if true, hs2 joins and then leaves the room before the invite is sent, so it has a stale
		// state DAG for the room rather than no state at all.
		withPriorState bool
		// if true the invite is rescinded by the inviter, else it is refused by the invited user.
		rescind bool
	}{
		{name: "invited user can refuse an out-of-band invite"},
		{name: "invited user can refuse an out-of-band invite with prior room state", withPriorState: true},
		{name: "inviter can rescind an out-of-band invite", rescind: true},
		{name: "inviter can rescind an out-of-band invite with prior room state", withPriorState: true, rescind: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
			bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"room_version": roomVersion,
				"preset":       "private_chat",
			})
			if tc.withPriorState {
				// Bob2 joins then leaves, so hs2 has the state DAG for this room up to Bob2's leave,
				// but is no longer in the room.
				bob2 := deployment.Register(t, "hs2", helpers.RegistrationOpts{})
				alice.MustInviteRoom(t, roomID, bob2.UserID)
				bob2.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob2.UserID, roomID))
				bob2.MustJoinRoom(t, roomID, []spec.ServerName{hs1})
				bob2Joined := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob2.UserID, roomID))
				bob2.MustLeaveRoom(t, roomID)
				alice.MustSyncUntil(t, client.SyncReq{Since: bob2Joined}, client.SyncLeftFrom(bob2.UserID, roomID))
			}
			// Move the state DAG on so the invite references state events hs2 does not have. In the
			// prior state case hs2 has left by now so it will not be told about these.
			changeDisplayName(t, alice, roomID, "alice", 3)

			// Remember where both users are in the sync stream: leaving a room you were only invited
			// to is visible in an incremental sync, but not in an initial one.
			aliceSince := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
			alice.MustInviteRoom(t, roomID, bob.UserID)
			bobSince := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))

			if tc.rescind {
				// Alice, the inviter, rescinds the invite.
				alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "kick"},
					client.WithJSONBody(t, map[string]interface{}{
						"user_id": bob.UserID,
						"reason":  "rescinding the invite",
					}),
				)
			} else {
				// Bob refuses the invite. hs2 cannot calculate the prev_state_events for the leave
				// event itself, so it has to ask hs1 via /make_leave.
				bob.MustLeaveRoom(t, roomID)
			}
			bob.MustSyncUntil(t, client.SyncReq{Since: bobSince}, client.SyncLeftFrom(bob.UserID, roomID))
			alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncLeftFrom(bob.UserID, roomID))
		})
	}
}

// changeDisplayName changes the display name of cli numTimes, waiting for each change to land in
// roomID before making the next one. Servers may propagate profile changes into rooms
// asynchronously (Synapse does this in a background task) so if we don't wait, several changes can
// collapse into a single m.room.member event, shortening the state DAG.
func changeDisplayName(t *testing.T, cli *client.CSAPI, roomID, prefix string, numTimes int) {
	t.Helper()
	for i := 0; i < numTimes; i++ {
		displayName := fmt.Sprintf("%s %d", prefix, i)
		cli.MustSetDisplayName(t, displayName)
		cli.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
			return ev.Get("type").Str == "m.room.member" && ev.Get("state_key").Str == cli.UserID &&
				ev.Get("content.displayname").Str == displayName
		}))
	}
}
