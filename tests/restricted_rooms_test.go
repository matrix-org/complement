// Tests MSC3083, joining restricted rooms based on membership in another room.

package tests

import (
	"net/url"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/matrix-org/complement/runtime"
)

func failJoinRoom(t *testing.T, c *client.CSAPI, roomIDOrAlias string, serverName string) {
	t.Helper()

	// This is copied from Client.JoinRoom to test a join failure.
	query := make(url.Values, 1)
	query.Set("server_name", serverName)
	res := c.DoFunc(
		t,
		"POST",
		[]string{"_matrix", "client", "v3", "join", roomIDOrAlias},
		client.WithQueries(query),
	)
	must.MatchFailure(t, res)
}

// Creates two rooms on room version 8 and sets the second room to have
// restricted join rules with allow set to the first room.
func setupRestrictedRoom(t *testing.T, deployment *docker.Deployment, roomVersion string, joinRule string) (*client.CSAPI, string, string) {
	t.Helper()

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	// The room which membership checks are delegated to. In practice, this will
	// often be an MSC1772 space, but that is not required.
	allowed_room := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "Allowed Room",
	})
	// The room is room version 8 which supports the restricted join_rule.
	room := alice.CreateRoom(t, map[string]interface{}{
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
							"via":     []string{"hs1"},
						},
					},
				},
			},
		},
	})

	return alice, allowed_room, room
}

func checkRestrictedRoom(t *testing.T, alice *client.CSAPI, bob *client.CSAPI, allowed_room string, room string, joinRule string) {
	t.Helper()

	t.Run("Join should fail initially", func(t *testing.T) {
		failJoinRoom(t, bob, room, "hs1")
	})

	t.Run("Join should succeed when joined to allowed room", func(t *testing.T) {
		// Join the allowed room.
		bob.JoinRoom(t, allowed_room, []string{"hs1"})

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
		bob.JoinRoom(t, room, []string{"hs1"})

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
		bob.LeaveRoom(t, room)
		bob.LeaveRoom(t, allowed_room)

		// Wait until Alice sees Bob leave the allowed room. This ensures that Alice's HS
		// has processed the leave before Bob tries rejoining, so that it rejects his
		// attempt to join the room.
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(
			allowed_room, func(ev gjson.Result) bool {
				if ev.Get("type").Str != "m.room.member" || ev.Get("sender").Str != bob.UserID {
					return false
				}

				return ev.Get("content").Get("membership").Str == "leave"
			}))

		failJoinRoom(t, bob, room, "hs1")
	})

	t.Run("Join should succeed when invited", func(t *testing.T) {
		// Invite the user and joining should work.
		alice.InviteRoom(t, room, bob.UserID)
		bob.JoinRoom(t, room, []string{"hs1"})

		// Leave the room again, and join the allowed room.
		bob.LeaveRoom(t, room)
		bob.JoinRoom(t, allowed_room, []string{"hs1"})
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
		failJoinRoom(t, bob, room, "hs1")

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
		failJoinRoom(t, bob, room, "hs1")
	})
}

// Test joining a room with join rules restricted to membership in another room.
func TestRestrictedRoomsLocalJoin(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	// Setup the user, allowed room, and restricted room.
	alice, allowed_room, room := setupRestrictedRoom(t, deployment, "8", "restricted")

	// Create a second user on the same homeserver.
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	// Execute the checks.
	checkRestrictedRoom(t, alice, bob, allowed_room, room, "restricted")
}

// Test joining a room with join rules restricted to membership in another room.
func TestRestrictedRoomsRemoteJoin(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationOneToOneRoom)
	defer deployment.Destroy(t)

	// Setup the user, allowed room, and restricted room.
	alice, allowed_room, room := setupRestrictedRoom(t, deployment, "8", "restricted")

	// Create a second user on a different homeserver.
	bob := deployment.Client(t, "hs2", "@bob:hs2")

	// Execute the checks.
	checkRestrictedRoom(t, alice, bob, allowed_room, room, "restricted")
}

// A server will do a remote join for a local user if it is unable to to issue
// joins in a restricted room it is already participating in.
func TestRestrictedRoomsRemoteJoinLocalUser(t *testing.T) {
	doTestRestrictedRoomsRemoteJoinLocalUser(t, "8", "restricted")
}

func doTestRestrictedRoomsRemoteJoinLocalUser(t *testing.T, roomVersion string, joinRule string) {
	runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/2801

	deployment := Deploy(t, b.BlueprintFederationTwoLocalOneRemote)
	defer deployment.Destroy(t)

	// Charlie sets up the allowed room so it is on the other server.
	//
	// This is the room which membership checks are delegated to. In practice,
	// this will often be an MSC1772 space, but that is not required.
	charlie := deployment.Client(t, "hs2", "@charlie:hs2")
	allowed_room := charlie.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "Space",
	})
	// The room is room version 8 which supports the restricted join_rule.
	room := charlie.CreateRoom(t, map[string]interface{}{
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
							"via":     []string{"hs2"},
						},
					},
				},
			},
		},
	})

	// Invite alice manually and accept it.
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	charlie.InviteRoom(t, room, alice.UserID)
	alice.JoinRoom(t, room, []string{"hs2"})

	// Confirm that Alice cannot issue invites (due to the default power levels).
	bob := deployment.Client(t, "hs1", "@bob:hs1")
	body := map[string]interface{}{
		"user_id": bob.UserID,
	}
	res := alice.DoFunc(
		t,
		"POST",
		[]string{"_matrix", "client", "v3", "rooms", room, "invite"},
		client.WithJSONBody(t, body),
	)
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 403,
	})

	// Bob cannot join the room.
	failJoinRoom(t, bob, room, "hs1")

	// Join the allowed room via hs2.
	bob.JoinRoom(t, allowed_room, []string{"hs2"})
	// Joining the room should work, although we're joining via hs1, it will end up
	// as a remote join through hs2.
	bob.JoinRoom(t, room, []string{"hs1"})

	// Ensure that the join comes down sync on hs2. Note that we want to ensure hs2
	// accepted the event.
	charlie.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(
		room,
		func(ev gjson.Result) bool {
			if ev.Get("type").Str != "m.room.member" || ev.Get("state_key").Str != bob.UserID {
				return false
			}
			must.EqualStr(t, ev.Get("sender").Str, bob.UserID, "Bob should have joined by himself")
			must.EqualStr(t, ev.Get("content").Get("membership").Str, "join", "Bob failed to join the room")

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
	charlie.LeaveRoom(t, room)

	// Ensure the events have synced to hs1.
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(
		room,
		func(ev gjson.Result) bool {
			if ev.Get("type").Str != "m.room.member" || ev.Get("state_key").Str != charlie.UserID {
				return false
			}
			must.EqualStr(t, ev.Get("content").Get("membership").Str, "leave", "Charlie failed to leave the room")

			return true
		},
	))

	// Have bob leave and rejoin. This should still work even though hs2 isn't in
	// the room anymore!
	bob.LeaveRoom(t, room)
	bob.JoinRoom(t, room, []string{"hs1"})
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

	deployment := Deploy(t, b.Blueprint{
		Name: "federation_three_homeservers",
		Homeservers: []b.Homeserver{
			{
				Name: "hs1",
				Users: []b.User{
					{
						Localpart:   "alice",
						DisplayName: "Alice",
					},
				},
			},
			{
				Name: "hs2",
				Users: []b.User{
					{
						Localpart:   "bob",
						DisplayName: "Bob",
					},
				},
			},
			{
				Name: "hs3",
				Users: []b.User{
					{
						Localpart:   "charlie",
						DisplayName: "Charlie",
					},
				},
			},
		},
	})
	defer deployment.Destroy(t)

	// Setup the user, allowed room, and restricted room.
	alice, allowed_room, room := setupRestrictedRoom(t, deployment, roomVersion, joinRule)

	// Raise the power level so that only alice can invite.
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
	bob := deployment.Client(t, "hs2", "@bob:hs2")

	// Bob joins the room and allowed room.
	bob.JoinRoom(t, allowed_room, []string{"hs1"})
	bob.JoinRoom(t, room, []string{"hs1"})

	// Charlie should join the allowed room (which gives access to the room).
	charlie := deployment.Client(t, "hs3", "@charlie:hs3")
	charlie.JoinRoom(t, allowed_room, []string{"hs1"})

	// hs2 doesn't have anyone to invite from, so the join fails.
	failJoinRoom(t, charlie, room, "hs2")

	// Including hs1 (and failing over to it) allows the join to succeed.
	charlie.JoinRoom(t, room, []string{"hs2", "hs1"})

	// Double check that the join was authorised via hs1.
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(
		room,
		func(ev gjson.Result) bool {
			if ev.Get("type").Str != "m.room.member" || ev.Get("state_key").Str != charlie.UserID {
				return false
			}
			must.EqualStr(t, ev.Get("content").Get("membership").Str, "join", "Charlie failed to join the room")
			must.EqualStr(t, ev.Get("content").Get("join_authorised_via_users_server").Str, alice.UserID, "Join authorised via incorrect server")

			return true
		},
	))

	// Bump the power-level of bob.
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
	charlie.LeaveRoom(t, room)

	// Ensure the events have synced to hs1 and hs2, otherwise the joins below may
	// happen before the leaves, from the perspective of hs1 and hs2.
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(charlie.UserID, room))
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(charlie.UserID, room))

	// Bob leaves the allowed room so that hs2 doesn't know if Charlie is in the
	// allowed room or not.
	bob.LeaveRoom(t, allowed_room)

	// hs2 cannot complete the join since they do not know if Charlie meets the
	// requirements (since it is no longer in the allowed room).
	failJoinRoom(t, charlie, room, "hs2")

	// Including hs1 (and failing over to it) allows the join to succeed.
	charlie.JoinRoom(t, room, []string{"hs2", "hs1"})

	// Double check that the join was authorised via hs1.
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(
		room,
		func(ev gjson.Result) bool {
			if ev.Get("type").Str != "m.room.member" || ev.Get("state_key").Str != charlie.UserID {
				return false
			}
			must.EqualStr(t, ev.Get("content").Get("membership").Str, "join", "Charlie failed to join the room")
			must.EqualStr(t, ev.Get("content").Get("join_authorised_via_users_server").Str, alice.UserID, "Join authorised via incorrect server")

			return true
		},
	))
}
