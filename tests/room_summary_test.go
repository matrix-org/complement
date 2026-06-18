// Tests the GET /_matrix/client/v1/room_summary/{roomIdOrAlias} endpoint
// as specified in https://spec.matrix.org/v1.15/client-server-api/#get_matrixclientv1room_summaryroomidoralias

package tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

// TestRoomSummaryAllowedRoomIDs checks that the /summary endpoint includes
// allowed_room_ids for rooms with restricted join rules, and omits the field
// for rooms that do not use restricted join rules.
func TestRoomSummaryAllowedRoomIDs(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	// Create a space that will be referenced by the restricted room.
	space := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"creation_content": map[string]interface{}{
			"type": "m.space",
		},
	})

	// Create a room version 8 room with restricted join rules allowing members
	// of the space above.
	restrictedRoom := alice.MustCreateRoom(t, map[string]interface{}{
		"preset":       "public_chat",
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
						},
					},
				},
			},
		},
	})

	// Create an invite-only room.
	inviteRoom := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "private_chat",
	})

	t.Run("restricted room includes allowed_room_ids", func(t *testing.T) {
		res := alice.MustDo(t, "GET", []string{"_matrix", "client", "v1", "room_summary", restrictedRoom})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyEqual("room_id", restrictedRoom),
				match.JSONKeyEqual("join_rule", "restricted"),
				match.JSONKeyEqual("allowed_room_ids", []interface{}{space}),
			},
		})
	})

	t.Run("non-restricted room omits allowed_room_ids", func(t *testing.T) {
		res := alice.MustDo(t, "GET", []string{"_matrix", "client", "v1", "room_summary", inviteRoom})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyEqual("room_id", inviteRoom),
				match.JSONKeyMissing("allowed_room_ids"),
			},
		})
	})
}
