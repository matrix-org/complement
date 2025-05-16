// Tests MSC3083, joining restricted spaces based on membership in another room.

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
	"github.com/matrix-org/gomatrixserverlib/spec"
)

// Request the room summary and ensure the expected rooms are in the response.
func requestAndAssertSummary(t *testing.T, user *client.CSAPI, space string, expected_rooms []interface{}) {
	t.Helper()

	res := user.MustDo(t, "GET", []string{"_matrix", "client", "v1", "rooms", space, "hierarchy"})
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONCheckOffDeprecated("rooms", expected_rooms, func(r gjson.Result) interface{} {
				return r.Get("room_id").Str
			}, nil),
		},
	})
}

// Tests that MSC2946 works for a restricted room.
//
// Create a space with a room in it that has join rules restricted to membership
// in that space.
//
// The user should be unable to see the room in the spaces summary unless they
// are a member of the space.
func TestRestrictedRoomsSpacesSummaryLocal(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	// Create the rooms
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	space := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "Space",
		"creation_content": map[string]interface{}{
			"type": "m.space",
		},
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
	// The room is room version 8 which supports the restricted join_rule.
	room := alice.MustCreateRoom(t, map[string]interface{}{
		"preset":       "public_chat",
		"name":         "Room",
		"room_version": "8",
		"initial_state": []map[string]interface{}{
			{
				"type":      "m.room.join_rules",
				"state_key": "",
				"content": map[string]interface{}{
					"join_rule": "restricted",
					"allow": []map[string]interface{}{
						{
							"type":    "m.room_membership",
							"room_id": &space,
							"via":     []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs1"))},
						},
					},
				},
			},
		},
	})
	alice.SendEventSynced(t, space, b.Event{
		Type:     "m.space.child",
		StateKey: &room,
		Content: map[string]interface{}{
			"via": []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs1"))},
		},
	})

	t.Logf("Space: %s", space)
	t.Logf("Room: %s", room)

	// Create a second user on the same homeserver.
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	// Querying the space returns only the space, as the room is restricted.
	requestAndAssertSummary(t, bob, space, []interface{}{space})

	// Join the space, and now the restricted room should appear.
	bob.MustJoinRoom(t, space, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
	})
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, space))
	requestAndAssertSummary(t, bob, space, []interface{}{space, room})
}

// Tests that MSC2946 works over federation for a restricted room.
//
// Create a space with a room in it that has join rules restricted to membership
// in that space. The space and room are on different homeservers. While generating
// the summary of space hs1 needs to ask hs2 to generate the summary for room since
// it is not participating in the room.
//
// The user should be unable to see the room in the spaces summary unless they
// are a member of the space.
//
// This tests the interactions over federation where the space and room are on
// different homeservers, and one might not have the proper information needed to
// decide if a user is in a room.
func TestRestrictedRoomsSpacesSummaryFederation(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	// Create the rooms
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	space := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "Space",
		"creation_content": map[string]interface{}{
			"type": "m.space",
		},
		"initial_state": []map[string]interface{}{
			{
				"type":      "m.room.history_visibility",
				"state_key": "",
				"content": map[string]string{
					"history_visibility": "world_readable",
				},
			},
		},
	})

	// The room is room version 8 which supports the restricted join_rule and is
	// created on hs2.
	charlie := deployment.Register(t, "hs2", helpers.RegistrationOpts{})
	room := charlie.MustCreateRoom(t, map[string]interface{}{
		"preset":       "public_chat",
		"name":         "Room",
		"room_version": "8",
		"initial_state": []map[string]interface{}{
			{
				"type":      "m.room.join_rules",
				"state_key": "",
				"content": map[string]interface{}{
					"join_rule": "restricted",
					"allow": []map[string]interface{}{
						{
							"type":    "m.room_membership",
							"room_id": &space,
							"via":     []string{"hs1"},
						},
					},
				},
			},
		},
	})

	// create the link (this doesn't really make sense since how would alice know
	// about the room? but it works for testing)
	alice.SendEventSynced(t, space, b.Event{
		Type:     spaceChildEventType,
		StateKey: &room,
		Content: map[string]interface{}{
			"via": []string{"hs2"},
		},
	})

	// The room appears for neither alice or bob initially. Although alice is in
	// the space and should be able to access the room, hs2 doesn't know this!
	requestAndAssertSummary(t, alice, space, []interface{}{space})
	requestAndAssertSummary(t, bob, space, []interface{}{space})

	// charlie joins the space and now hs2 knows that alice is in the space (and
	// can join the room).
	charlie.MustJoinRoom(t, space, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
	})
	charlie.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(charlie.UserID, space))

	// The restricted room should appear for alice (who is in the space).
	requestAndAssertSummary(t, alice, space, []interface{}{space, room})
	requestAndAssertSummary(t, bob, space, []interface{}{space})
}
