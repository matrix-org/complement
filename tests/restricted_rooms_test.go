// Tests MSC3083, joining restricted rooms based on membership in another room.

package tests

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
	"github.com/matrix-org/gomatrixserverlib/spec"
)

// Creates two rooms on room version 8 and sets the second room to have
// restricted join rules with allow set to the first room.
func setupRestrictedRoom(t *testing.T, deployment complement.Deployment, roomVersion string, joinRule string) (*client.CSAPI, string, string) {
	t.Helper()

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	// The room which membership checks are delegated to. In practice, this will
	// often be an MSC1772 space, but that is not required.
	allowed_room := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "Allowed Room",
	})
	// The room is room version 8 which supports the restricted join_rule.
	room := alice.MustCreateRoom(t, map[string]interface{}{
		"preset":       "public_chat",
		"name":         "Room",
		"room_version": roomVersion,
		"initial_state": []map[string]interface{}{
			{
				"type":      "m.room.join_rules",
				"state_key": "",
				"content": map[string]interface{}{
					"join_rule": joinRule,
					"allow": []map[string]interface{}{
						{
							"type":    "m.room_membership",
							"room_id": &allowed_room,
							"via":     []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs1"))},
						},
					},
				},
			},
		},
	})

	return alice, allowed_room, room
}

func checkRestrictedRoom(t *testing.T, deployment complement.Deployment, alice *client.CSAPI, bob *client.CSAPI, allowed_room string, room string, joinRule string) {
	t.Helper()

	t.Run("Join should fail initially", func(t *testing.T) {
		res := bob.JoinRoom(t, room, []spec.ServerName{
			deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
		})
		must.MatchFailure(t, res)
	})

	t.Run("Join should succeed when joined to allowed room", func(t *testing.T) {
		// Join the allowed room.
		bob.JoinRoom(t, allowed_room, []spec.ServerName{
			deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
		})

		// Confirm that we joined the allowed room by changing displayname and
		// waiting for confirmation in the /sync response. (This is an attempt
		// to mitigate race conditions between Synapse workers. We want to
		// ensure that the worker serving the join to `room` knows we are joined
		// to `allowed_room`.)
		bob.SendEventSynced(
			t,
			allowed_room,
			b.Event{
				Type:     "m.room.member",
				Sender:   bob.UserID,
				StateKey: &bob.UserID,
				Content: map[string]interface{}{
					"membership":  "join",
					"displayname": "Bobby",
				},
			},
		)

		// We should now be able to join the restricted room.
		bob.JoinRoom(t, room, []spec.ServerName{
			deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
		})

		// Joining the same room again should work fine (e.g. to change your display name).
		bob.SendEventSynced(
			t,
			room,
			b.Event{
				Type:     "m.room.member",
				Sender:   bob.UserID,
				StateKey: &bob.UserID,
				Content: map[string]interface{}{
					"membership":  "join",
					"displayname": "Bobby",
					// This should be ignored since this is a join -> join transition.
					"join_authorised_via_users_server": "unused",
				},
			},
		)
	})

	t.Run("Join should fail when left allowed room", func(t *testing.T) {
		// Leaving the room works and the user is unable to re-join.
		bob.MustLeaveRoom(t, room)
		bob.MustLeaveRoom(t, allowed_room)

		// Wait until Alice sees Bob leave the allowed room. This ensures that Alice's HS
		// has processed the leave before Bob tries rejoining, so that it rejects his
		// attempt to join the room.
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(bob.UserID, allowed_room))

		res := bob.JoinRoom(t, room, []spec.ServerName{
			deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
		})
		must.MatchFailure(t, res)
	})

	t.Run("Join should succeed when invited", func(t *testing.T) {
		// Invite the user and joining should work.
		alice.MustInviteRoom(t, room, bob.UserID)
		bob.JoinRoom(t, room, []spec.ServerName{
			deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
		})

		// Leave the room again, and join the allowed room.
		bob.MustLeaveRoom(t, room)
		bob.JoinRoom(t, allowed_room, []spec.ServerName{
			deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
		})
	})

	t.Run("Join should fail with mangled join rules", func(t *testing.T) {
		// Update the room to have bad values in the "allow" field, which should stop
		// joining from working properly.
		emptyStateKey := ""
		alice.SendEventSynced(
			t,
			room,
			b.Event{
				Type:     "m.room.join_rules",
				Sender:   alice.UserID,
				StateKey: &emptyStateKey,
				Content: map[string]interface{}{
					"join_rule": joinRule,
					"allow":     []string{"invalid"},
				},
			},
		)
		// Fails since invalid values get filtered out of allow.
		must.MatchFailure(t, bob.JoinRoom(t, room, []spec.ServerName{
			deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
		}))

		alice.SendEventSynced(
			t,
			room,
			b.Event{
				Type:     "m.room.join_rules",
				Sender:   alice.UserID,
				StateKey: &emptyStateKey,
				Content: map[string]interface{}{
					"join_rule": joinRule,
					"allow":     "invalid",
				},
			},
		)
		// Fails since a fully invalid allow key requires an invite.
		must.MatchFailure(t, bob.JoinRoom(t, room, []spec.ServerName{
			deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
		}))
	})
}

// Test joining a room with join rules restricted to membership in another room.
func TestRestrictedRoomsLocalJoin(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	// Setup the user, allowed room, and restricted room.
	alice, allowed_room, room := setupRestrictedRoom(t, deployment, "8", "restricted")

	// Create a second user on the same homeserver.
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	// Execute the checks.
	checkRestrictedRoom(t, deployment, alice, bob, allowed_room, room, "restricted")
}

// Test joining a room with join rules restricted to membership in another room.
func TestRestrictedRoomsRemoteJoin(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	// Setup the user, allowed room, and restricted room.
	alice, allowed_room, room := setupRestrictedRoom(t, deployment, "8", "restricted")

	// Create a second user on a different homeserver.
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})

	// Execute the checks.
	checkRestrictedRoom(t, deployment, alice, bob, allowed_room, room, "restricted")
}

// A server will do a remote join for a local user if it is unable to to issue
// joins in a restricted room it is already participating in.
func TestRestrictedRoomsRemoteJoinLocalUser(t *testing.T) {
	doTestRestrictedRoomsRemoteJoinLocalUser(t, "8", "restricted")
}

func doTestRestrictedRoomsRemoteJoinLocalUser(t *testing.T, roomVersion string, joinRule string) {
	runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/2801

	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	// Charlie sets up the allowed room so it is on the other server.
	//
	// This is the room which membership checks are delegated to. In practice,
	// this will often be an MSC1772 space, but that is not required.
	charlie := deployment.Register(t, "hs2", helpers.RegistrationOpts{})
	allowed_room := charlie.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "Space",
	})
	// The room is room version 8 which supports the restricted join_rule.
	room := charlie.MustCreateRoom(t, map[string]interface{}{
		"preset":       "public_chat",
		"name":         "Room",
		"room_version": roomVersion,
		"initial_state": []map[string]interface{}{
			{
				"type":      "m.room.join_rules",
				"state_key": "",
				"content": map[string]interface{}{
					"join_rule": joinRule,
					"allow": []map[string]interface{}{
						{
							"type":    "m.room_membership",
							"room_id": &allowed_room,
							"via":     []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs2"))},
						},
					},
				},
			},
		},
	})

	// Invite alice manually and accept it.
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	charlie.MustInviteRoom(t, room, alice.UserID)
	alice.JoinRoom(t, room, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs2"),
	})

	// Confirm that Alice cannot issue invites (due to the default power levels).
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	res := alice.InviteRoom(t, room, bob.UserID)
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 403,
	})

	// Bob cannot join the room.
	must.MatchFailure(t, bob.JoinRoom(t, room, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
	}))

	// Join the allowed room via hs2.
	bob.JoinRoom(t, allowed_room, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs2"),
	})
	// Joining the room should work, although we're joining via hs1, it will end up
	// as a remote join through hs2.
	bob.JoinRoom(t, room, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
	})

	// Ensure that the join comes down sync on hs2. Note that we want to ensure hs2
	// accepted the event.
	charlie.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(
		room,
		func(ev gjson.Result) bool {
			if ev.Get("type").Str != "m.room.member" || ev.Get("state_key").Str != bob.UserID {
				return false
			}
			must.Equal(t, ev.Get("sender").Str, bob.UserID, "Bob should have joined by himself")
			must.Equal(t, ev.Get("content").Get("membership").Str, "join", "Bob failed to join the room")

			return true
		},
	))

	// Raise the power level so that users on hs1 can invite people and then leave
	// the room.
	state_key := ""
	charlie.SendEventSynced(t, room, b.Event{
		Type:     "m.room.power_levels",
		StateKey: &state_key,
		Content: map[string]interface{}{
			"invite": 0,
			"users": map[string]interface{}{
				charlie.UserID: 100,
			},
		},
	})
	charlie.MustLeaveRoom(t, room)

	// Ensure the events have synced to hs1.
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(charlie.UserID, room))

	// Have bob leave and rejoin. This should still work even though hs2 isn't in
	// the room anymore!
	bob.MustLeaveRoom(t, room)
	bob.JoinRoom(t, room, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
	})
}

// A server will request a failover if asked to /make_join and it does not have
// the appropriate authorisation to complete the request.
//
// Setup 3 homeservers:
// * hs1 creates the allowed room/room.
// * hs2 joins the room
// * hs3 attempts to join via hs2 (should fail) and hs1 (should work)
func TestRestrictedRoomsRemoteJoinFailOver(t *testing.T) {
	doTestRestrictedRoomsRemoteJoinFailOver(t, "8", "restricted")
}

func doTestRestrictedRoomsRemoteJoinFailOver(t *testing.T, roomVersion string, joinRule string) {
	runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/2801

	deployment := complement.Deploy(t, 3)
	defer deployment.Destroy(t)

	// Setup the user, allowed room, and restricted room.
	alice, allowed_room, room := setupRestrictedRoom(t, deployment, roomVersion, joinRule)
	t.Logf("%s created authorizing room %s.", alice.UserID, allowed_room)
	t.Logf("%s created restricted room %s.", alice.UserID, room)

	// Raise the power level so that only alice can invite.
	t.Logf("%s restricts invites to themself only.", alice.UserID)
	state_key := ""
	alice.SendEventSynced(t, room, b.Event{
		Type:     "m.room.power_levels",
		StateKey: &state_key,
		Content: map[string]interface{}{
			"invite": 100,
			"users": map[string]interface{}{
				alice.UserID: 100,
			},
		},
	})

	// Create a second user on a different homeserver.
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})

	// Bob joins the room and allowed room.
	t.Logf("%s joins the authorizing room via hs1.", bob.UserID)
	t.Logf("%s joins the restricted room via hs1.", bob.UserID)
	bob.JoinRoom(t, allowed_room, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
	})
	bob.JoinRoom(t, room, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
	})

	// Charlie should join the allowed room (which gives access to the room).
	charlie := deployment.Register(t, "hs3", helpers.RegistrationOpts{})
	t.Logf("%s joins the authorizing room via hs1.", charlie.UserID)
	charlie.JoinRoom(t, allowed_room, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
	})

	// hs2 doesn't have anyone to invite from, so the join fails.
	t.Logf("%s joins the restricted room via hs2, which is expected to fail.", charlie.UserID)
	res := charlie.JoinRoom(t, room, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs2"),
	})
	must.MatchFailure(t, res)

	// Including hs1 (and failing over to it) allows the join to succeed.
	t.Logf("%s joins the restricted room via {hs2,hs1}.", charlie.UserID)
	charlie.JoinRoom(t, room, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs2"),
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
	})

	// Double check that the join was authorised via hs1.
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(
		room,
		func(ev gjson.Result) bool {
			if ev.Get("type").Str != "m.room.member" || ev.Get("state_key").Str != charlie.UserID {
				return false
			}
			must.Equal(t, ev.Get("content").Get("membership").Str, "join", "Charlie failed to join the room")
			must.Equal(t, ev.Get("content").Get("join_authorised_via_users_server").Str, alice.UserID, "Join authorised via incorrect server")

			return true
		},
	))

	// Bump the power-level of bob.
	t.Logf("%s allows %s to send invites.", alice.UserID, bob.UserID)
	alice.SendEventSynced(t, room, b.Event{
		Type:     "m.room.power_levels",
		StateKey: &state_key,
		Content: map[string]interface{}{
			"invite": 100,
			"users": map[string]interface{}{
				alice.UserID: 100,
				bob.UserID:   100,
			},
		},
	})

	// Charlie leaves the room (so they can rejoin).
	t.Logf("%s leaves the restricted room.", charlie.UserID)
	charlie.MustLeaveRoom(t, room)

	// Ensure the events have synced to hs1 and hs2, otherwise the joins below may
	// happen before the leaves, from the perspective of hs1 and hs2.
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(charlie.UserID, room))
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(charlie.UserID, room))

	// Bob leaves the allowed room so that hs2 doesn't know if Charlie is in the
	// allowed room or not.
	t.Logf("%s leaves the authorizing room.", bob.UserID)
	bob.MustLeaveRoom(t, allowed_room)

	// hs2 cannot complete the join since they do not know if Charlie meets the
	// requirements (since it is no longer in the allowed room).
	t.Logf("%s joins the restricted room via hs2, which is expected to fail.", charlie.UserID)
	must.MatchFailure(t, charlie.JoinRoom(t, room, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs2"),
	}))

	// Including hs1 (and failing over to it) allows the join to succeed.
	t.Logf("%s joins the restricted room via {hs2,hs1}.", charlie.UserID)
	charlie.JoinRoom(t, room, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs2"),
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
	})

	// Double check that the join was authorised via hs1.
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(
		room,
		func(ev gjson.Result) bool {
			if ev.Get("type").Str != "m.room.member" || ev.Get("state_key").Str != charlie.UserID {
				return false
			}
			must.MatchGJSON(t, ev,
				match.JSONKeyEqual("content.membership", "join"),
				match.JSONKeyEqual("content.join_authorised_via_users_server", alice.UserID),
			)
			return true
		},
	))
}
