// +build msc2946,msc3083

// Tests MSC3083, an experimental feature for joining restricted rooms based on
// membership in a space.

package tests

import (
	"net/url"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
)

var (
	msc1772SpaceChildEventType = "org.matrix.msc1772.space.child"
)

func FailJoinRoom(c *client.CSAPI, t *testing.T, roomIDOrAlias string, serverName string) {
	// This is copied from Client.JoinRoom to test a join failure.
	query := make(url.Values, 1)
	query.Set("server_name", serverName)
	c.MustDoWithStatusRaw(
		t,
		"POST",
		[]string{"_matrix", "client", "r0", "join", roomIDOrAlias},
		nil,
		"application/json",
		query,
		403,
	)
}

// Create a space and put a room in it which is set to:
// * The experimental room version.
// * restricted join rules with allow set to the space.
func SetupRestrictedRoom(t *testing.T, deployment *docker.Deployment) (*client.CSAPI, string, string) {
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	space := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "Space",
	})
	// The room is an unstable room version which supports the restricted join_rule.
	room := alice.CreateRoom(t, map[string]interface{}{
		"preset":       "public_chat",
		"name":         "Room",
		"room_version": "org.matrix.msc3083",
		"initial_state": []map[string]interface{}{
			{
				"type":      "m.room.join_rules",
				"state_key": "",
				"content": map[string]interface{}{
					"join_rule": "restricted",
					"allow": []map[string]interface{}{
						{
							"space": &space,
							"via":   []string{"hs1"},
						},
					},
				},
			},
		},
	})
	alice.SendEventSynced(t, space, b.Event{
		Type:     msc1772SpaceChildEventType,
		StateKey: &room,
		Content: map[string]interface{}{
			"via": []string{"hs1"},
		},
	})

	return alice, space, room
}

func CheckRestrictedRoom(t *testing.T, alice *client.CSAPI, bob *client.CSAPI, space string, room string) {
	FailJoinRoom(bob, t, room, "hs1")

	// Join the space, attempt to join the room again, which now should succeed.
	bob.JoinRoom(t, space, []string{"hs1"})
	bob.JoinRoom(t, room, []string{"hs1"})

	// Leaving the room works and the user is unable to re-join.
	bob.LeaveRoom(t, room)
	bob.LeaveRoom(t, space)
	FailJoinRoom(bob, t, room, "hs1")

	// Invite the user and joining should work.
	alice.InviteRoom(t, room, bob.UserID)
	bob.JoinRoom(t, room, []string{"hs1"})

	// Leave the room again, and join the space.
	bob.LeaveRoom(t, room)
	bob.JoinRoom(t, space, []string{"hs1"})

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
				"join_rule": "restricted",
				"allow":     []string{"invalid"},
			},
		},
	)
	// Fails since invalid values get filtered out of allow.
	FailJoinRoom(bob, t, room, "hs1")

	alice.SendEventSynced(
		t,
		room,
		b.Event{
			Type:     "m.room.join_rules",
			Sender:   alice.UserID,
			StateKey: &emptyStateKey,
			Content: map[string]interface{}{
				"join_rule": "restricted",
				"allow":     "invalid",
			},
		},
	)
	// Fails since a fully invalid allow key requires an invite.
	FailJoinRoom(bob, t, room, "hs1")
}

// Test joining a room with join rules restricted to membership in a space.
func TestRestrictedRoomsLocalJoin(t *testing.T) {
	deployment := Deploy(t, "msc3083_local", b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	// Setup the user, space, and restricted room.
	alice, space, room := SetupRestrictedRoom(t, deployment)

	// Create a second user on the same homeserver.
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	// Execute the checks.
	CheckRestrictedRoom(t, alice, bob, space, room)
}

// Test joining a room with join rules restricted to membership in a space.
func TestRestrictedRoomsRemoteJoin(t *testing.T) {
	deployment := Deploy(t, "msc3083_remote", b.BlueprintFederationOneToOneRoom)
	defer deployment.Destroy(t)

	// Setup the user, space, and restricted room.
	alice, space, room := SetupRestrictedRoom(t, deployment)

	// Create a second user on a different homeserver.
	bob := deployment.Client(t, "hs2", "@bob:hs2")

	// Execute the checks.
	CheckRestrictedRoom(t, alice, bob, space, room)
}

// Tests that MSC2946 works for a restricted room.
//
// Create a space with a room in it that has join rules restricted to membership
// in that space.
//
// The user should be unable to see the room in the spaces summary unless they
// are a member of the space.
func TestRestrictedRoomsSpacesSummary(t *testing.T) {
	deployment := Deploy(t, "msc2946", b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	// Create the rooms
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	space := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "Space",
		// World readable to allow peeking without joining.
		"initial_state": []map[string]interface{}{
			{
				"type":      "m.room.history_visibility",
				"state_key": "",
				"content": map[string]interface{}{
					"history_visibility": "world_readable",
				},
			},
		},
	})
	// The room is an unstable room version which supports the restricted join_rule.
	room := alice.CreateRoom(t, map[string]interface{}{
		"preset":       "public_chat",
		"name":         "Room",
		"room_version": "org.matrix.msc3083",
		"initial_state": []map[string]interface{}{
			{
				"type":      "m.room.join_rules",
				"state_key": "",
				"content": map[string]interface{}{
					"join_rule": "restricted",
					"allow": []map[string]interface{}{
						{
							"space": &space,
							"via":   []string{"hs1"},
						},
					},
				},
			},
		},
	})
	alice.SendEventSynced(t, space, b.Event{
		Type:     "org.matrix.msc1772.space.child",
		StateKey: &room,
		Content: map[string]interface{}{
			"via": []string{"hs1"},
		},
	})

	t.Logf("Space: %s", space)
	t.Logf("Room: %s", room)

	// Create a second user on the same homeserver.
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	// Querying the space returns only the space, as the room is restricted.
	res := bob.MustDo(t, "POST", []string{"_matrix", "client", "unstable", "org.matrix.msc2946", "rooms", space, "spaces"}, map[string]interface{}{})
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONCheckOff("rooms", []interface{}{
				space,
			}, func(r gjson.Result) interface{} {
				return r.Get("room_id").Str
			}, nil),
		},
	})

	// Join the space, and now the restricted room should appear.
	bob.JoinRoom(t, space, []string{"hs1"})

	res = bob.MustDo(t, "POST", []string{"_matrix", "client", "unstable", "org.matrix.msc2946", "rooms", space, "spaces"}, map[string]interface{}{})
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONCheckOff("rooms", []interface{}{
				space, room,
			}, func(r gjson.Result) interface{} {
				return r.Get("room_id").Str
			}, nil),
		},
	})
}
