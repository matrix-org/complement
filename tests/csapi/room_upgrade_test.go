package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/tidwall/gjson"
)

func TestPushRuleRoomUpgrade(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	alice2 := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})
	bob2 := deployment.Register(t, "hs2", helpers.RegistrationOpts{})

	t.Run("parallel", func(t *testing.T) {
		// When a homeserver becomes aware of a room upgrade (upgrade is done on local
		// homeserver), it should copy over any existing push rules for all of its local users
		// from the old room to the new room at the time of upgrade.
		t.Run("upgrading a room carries over existing push rules for local users", func(t *testing.T) {
			t.Parallel()

			// Create a room
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset":       "public_chat",
				"room_version": "10",
			})
			// Have alice2 join the room
			//
			// We use two users to ensure that all users get taken care of (not just the person
			// upgrading the room).
			alice2.MustJoinRoom(t, roomID, nil)

			// Add some push rules
			alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "pushrules", "global", "room", roomID},
				client.WithJSONBody(t, map[string]interface{}{
					"actions": []string{"dont_notify"},
				}),
			)
			alice2.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "pushrules", "global", "room", roomID},
				client.WithJSONBody(t, map[string]interface{}{
					"actions": []string{"dont_notify"},
				}),
			)

			// Sanity check the push rules are in the expected state before the upgrade
			for _, client := range []*client.CSAPI{alice, alice2} {
				pushRulesBefore := client.GetAllPushRules(t)
				must.MatchGJSON(t, pushRulesBefore,
					match.JSONCheckOff("global.room", []interface{}{
						roomID,
					},
						match.CheckOffMapper(func(r gjson.Result) interface{} { return r.Get("rule_id").Str }),
						match.CheckOffForEach(func(roomIDFromPushRule interface{}, result gjson.Result) error {
							return match.JSONKeyEqual("actions.0", "dont_notify")(result)
						}),
					),
				)
			}

			// Upgrade the room
			newRoomID := alice.MustUpgradeRoom(t, roomID, "11")

			// Alice2 joins the new room
			alice2.MustJoinRoom(t, newRoomID, nil)

			// Sanity check the push rules are in the expected state after the upgrade
			for _, client := range []*client.CSAPI{alice, alice2} {
				pushRulesAfter := client.GetAllPushRules(t)
				must.MatchGJSON(t, pushRulesAfter,
					match.JSONCheckOff("global.room", []interface{}{
						roomID,
						newRoomID,
					},
						match.CheckOffMapper(func(r gjson.Result) interface{} { return r.Get("rule_id").Str }),
						match.CheckOffForEach(func(roomIDFromPushRule interface{}, result gjson.Result) error {
							return match.JSONKeyEqual("actions.0", "dont_notify")(result)
						}),
					),
				)
			}
		})

		// When a homeserver becomes aware of a room upgrade (upgrade is done on remote
		// homeserver), it should copy over any existing push rules for all of its local users
		// from the old room to the new room at the time of upgrade.
		t.Run("joining a remote upgraded room carries over existing push rules", func(t *testing.T) {
			t.Parallel()

			// Alice create a room
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset":       "public_chat",
				"room_version": "10",
			})
			// Remote bob joins the room
			bob.MustJoinRoom(t, roomID, []spec.ServerName{
				deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
			})
			// Remote bob2 joins the room
			//
			// We use two users to ensure that all users get taken care of (not just the first
			// user on the homeserver).
			bob2.MustJoinRoom(t, roomID, []spec.ServerName{
				deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
			})

			// Add some push rules
			bob.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "pushrules", "global", "room", roomID},
				client.WithJSONBody(t, map[string]interface{}{
					"actions": []string{"dont_notify"},
				}),
			)
			bob2.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "pushrules", "global", "room", roomID},
				client.WithJSONBody(t, map[string]interface{}{
					"actions": []string{"dont_notify"},
				}),
			)

			// Sanity check the push rules are in the expected state before the upgrade
			for _, client := range []*client.CSAPI{bob, bob2} {
				pushRulesBefore := client.GetAllPushRules(t)
				must.MatchGJSON(t, pushRulesBefore,
					match.JSONCheckOff("global.room", []interface{}{
						roomID,
					},
						match.CheckOffMapper(func(r gjson.Result) interface{} { return r.Get("rule_id").Str }),
						match.CheckOffForEach(func(roomIDFromPushRule interface{}, result gjson.Result) error {
							return match.JSONKeyEqual("actions.0", "dont_notify")(result)
						}),
					),
				)
			}

			// Upgrade the room
			newRoomID := alice.MustUpgradeRoom(t, roomID, "11")

			// Remote bob joins the new room
			bob.MustJoinRoom(t, newRoomID, []spec.ServerName{
				deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
			})
			// Remote bob2 joins the new room
			bob2.MustJoinRoom(t, newRoomID, []spec.ServerName{
				deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
			})

			// Sanity check the push rules are in the expected state after the upgrade
			for _, client := range []*client.CSAPI{bob, bob2} {
				pushRulesAfter := client.GetAllPushRules(t)
				must.MatchGJSON(t, pushRulesAfter,
					match.JSONCheckOff("global.room", []interface{}{
						roomID,
						newRoomID,
					},
						match.CheckOffMapper(func(r gjson.Result) interface{} { return r.Get("rule_id").Str }),
						match.CheckOffForEach(func(roomIDFromPushRule interface{}, result gjson.Result) error {
							return match.JSONKeyEqual("actions.0", "dont_notify")(result)
						}),
					),
				)
			}
		})
	})
}
