package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

// This test ensures that an authorised (PL 100) user is able to modify the users_default value
// when that value is equal to the value of authorised user.
// Regression test for https://github.com/matrix-org/gomatrixserverlib/pull/306
func TestDemotingUsersViaUsersDefault(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"power_level_content_override": map[string]interface{}{
			"users_default": 100, // the default is 0
			"users": map[string]interface{}{
				alice.UserID: 100,
			},
			"events":        map[string]int64{},
			"notifications": map[string]int64{},
		},
	})

	alice.SendEventSynced(t, roomID, b.Event{
		Type:     "m.room.power_levels",
		StateKey: b.Ptr(""),
		Content: map[string]interface{}{
			"users_default": 40, // we change the default to 40. We should be able to do this.
			"users": map[string]interface{}{
				alice.UserID: 100,
			},
			"events":        map[string]int64{},
			"notifications": map[string]int64{},
		},
	})
}

func TestPowerLevels(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{})

	// sytest: GET /rooms/:room_id/state/m.room.power_levels can fetch levels
	t.Run("GET /rooms/:room_id/state/m.room.power_levels can fetch levels", func(t *testing.T) {
		// Test if the old state still exists
		res := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.power_levels"})

		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyPresent("users"),
			},
		})
	})

	// sytest: PUT /rooms/:room_id/state/m.room.power_levels can set levels
	t.Run("PUT /rooms/:room_id/state/m.room.power_levels can set levels", func(t *testing.T) {
		alice.SendEventSynced(t, roomID, b.Event{
			Type:     "m.room.power_levels",
			StateKey: b.Ptr(""),
			Content: map[string]interface{}{
				"invite": 100,
				"users": map[string]interface{}{
					alice.UserID: 100,
				},
			},
		})
	})

	// sytest: PUT power_levels should not explode if the old power levels were empty
	t.Run("PUT power_levels should not explode if the old power levels were empty", func(t *testing.T) {
		// Absence of an "events" key
		alice.SendEventSynced(t, roomID, b.Event{
			Type:     "m.room.power_levels",
			StateKey: b.Ptr(""),
			Content: map[string]interface{}{
				"users": map[string]interface{}{
					alice.UserID: 100,
				},
			},
		})

		// Absence of a "users" key
		alice.SendEventSynced(t, roomID, b.Event{
			Type:     "m.room.power_levels",
			StateKey: b.Ptr(""),
			Content:  map[string]interface{}{},
		})

		// This should give a 403 (not a 500)
		res := alice.DoFunc(
			t,
			"PUT",
			[]string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.power_levels"},
			client.WithJSONBody(t, map[string]interface{}{
				"users": map[string]string{},
			}),
		)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 403,
		})

		// Test if the old state still exists
		res = alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.power_levels"})

		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyMissing("users"),
			},
		})
	})
}
