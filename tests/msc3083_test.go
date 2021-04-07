// +build msc3083

// Tests MSC3083, an experimental feature for joining restricted rooms based on
// membership in a space.

package tests

import (
	"net/url"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
)

var (
	spaceChildEventType  = "org.matrix.msc1772.space.child"
	spaceParentEventType = "org.matrix.msc1772.space.parent"
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

// Test joining a room with join rules restricted to membership in a space.
func TestRestrictedRoomsLocalJoin(t *testing.T) {
	deployment := Deploy(t, "msc3083", b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	// Create the space and put a room in it.
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
		Type:     spaceChildEventType,
		StateKey: &room,
		Content: map[string]interface{}{
			"via": []string{"hs1"},
		},
	})

	// Create a second user and attempt to join the room, it should fail.
	bob := deployment.Client(t, "hs1", "@bob:hs1")
	FailJoinRoom(bob, t, room, "hs1")

	// Join the space, attempt to join the room again, which now should succeed.
	bob.JoinRoom(t, space, []string{"hs1"})
	bob.JoinRoom(t, room, []string{"hs1"})

	// Leaving the room works and the user is unable to re-join.
	bob.LeaveRoom(t, room)
	bob.LeaveRoom(t, space)
	FailJoinRoom(bob, t, room, "hs1")

	// Invite the user and joining should work.
	alice.InviteRoom(t, room, "@bob:hs1")
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
	// Succeeds since a fully invalid allow key is ignored (and the room is
	// treated as public).
	bob.JoinRoom(t, room, []string{"hs1"})
}
