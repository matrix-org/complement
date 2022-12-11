package csapi_tests

import (
	"fmt"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

// This test ensures that an authorised (PL 100) user is able to modify the users_default value
// when that value is equal to the value of authorised user.
// Regression test for https://github.com/matrix-org/gomatrixserverlib/pull/306
func TestDemotingUsersViaUsersDefault(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{})

	// sytest: GET /rooms/:room_id/state/m.room.power_levels can fetch levels
	t.Run("GET /rooms/:room_id/state/m.room.power_levels can fetch levels", func(t *testing.T) {
		// Test if the old state still exists
		res := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.power_levels"})

		// note: before v10 we technically cannot assume that powerlevel integers are json numbers,
		//  as they can be both strings and numbers.
		// However, for this test, we control the test environment,
		//  and we will assume the server is sane and give us powerlevels as numbers,
		//  and if it doesn't, that's an offense worthy of a frown.

		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyTypeEqual("ban", gjson.Number),
				match.JSONKeyTypeEqual("kick", gjson.Number),
				match.JSONKeyTypeEqual("redact", gjson.Number),
				match.JSONKeyTypeEqual("state_default", gjson.Number),
				match.JSONKeyTypeEqual("events_default", gjson.Number),
				match.JSONKeyTypeEqual("users_default", gjson.Number),

				match.JSONMapEach("events", func(k, v gjson.Result) error {
					if v.Type != gjson.Number {
						return fmt.Errorf("key %s is not a number", k.Str)
					} else {
						return nil
					}
				}),

				match.JSONMapEach("users", func(k, v gjson.Result) error {
					if v.Type != gjson.Number {
						return fmt.Errorf("key %s is not a number", k.Str)
					} else {
						return nil
					}
				}),

				func(body []byte) error {
					userDefault := int(gjson.GetBytes(body, "users_default").Num)
					thisUser := int(gjson.GetBytes(body, "users."+client.GjsonEscape(alice.UserID)).Num)

					if thisUser > userDefault {
						return nil
					} else {
						return fmt.Errorf("expected room creator (%d) to have a higher-than-default powerlevel (which is %d)", thisUser, userDefault)
					}
				},
			},
		})
	})

	// sytest: PUT /rooms/:room_id/state/m.room.power_levels can set levels
	t.Run("PUT /rooms/:room_id/state/m.room.power_levels can set levels", func(t *testing.T) {
		// note: these need to be floats to allow a roundtrip comparison
		PLContent := map[string]interface{}{
			"invite": 100.0,
			"users": map[string]interface{}{
				alice.UserID:                    100.0,
				"@random-other-user:their.home": 20.0,
			},
		}

		eventId := alice.SendEventSynced(t, roomID, b.Event{
			Type:     "m.room.power_levels",
			StateKey: b.Ptr(""),
			Content:  PLContent,
		})

		res := alice.MustDoFunc(
			t,
			"GET",
			[]string{"_matrix", "client", "v3", "rooms", roomID, "event", eventId},
		)

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("content", PLContent),
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
