package csapi_tests

import (
	"fmt"
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
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})

	for _, testCase := range []struct {
		name string
		// `content`, `room`, `sender`, `underride`, etc
		ruleset                 string
		pushRuleJsonBuilder     func(roomID string) map[string]interface{}
		pushRuleMatchersBuilder func(roomID string) []match.JSON
	}{
		{
			name:    "`room` ruleset",
			ruleset: "room",
			pushRuleJsonBuilder: func(roomID string) map[string]interface{} {
				return map[string]interface{}{
					"actions": []string{"dont_notify"},
				}
			},
			pushRuleMatchersBuilder: func(roomID string) []match.JSON {
				return []match.JSON{
					match.JSONKeyEqual("actions.0", "dont_notify"),
				}
			},
		},
		{
			name:    "`underride` ruleset",
			ruleset: "underride",
			pushRuleJsonBuilder: func(roomID string) map[string]interface{} {
				return map[string]interface{}{
					"conditions": []map[string]interface{}{
						{
							"key":     "room",
							"kind":    "event_match",
							"pattern": roomID,
						},
					},
					"actions": []string{"dont_notify"},
				}
			},
			pushRuleMatchersBuilder: func(roomID string) []match.JSON {
				return []match.JSON{
					match.JSONKeyEqual("actions.0", "dont_notify"),
					match.JSONKeyEqual("conditions.0.key", "room"),
					match.JSONKeyEqual("conditions.0.pattern", roomID),
				}
			},
		},
	} {
		// When a homeserver becomes aware of a room upgrade (upgrade is done on local
		// homeserver), it should copy over any push rules for all of its local users from the
		// old room to the new room.
		t.Run(fmt.Sprintf("upgrading a room carries over the push rules for local users (%s)", testCase.name), func(t *testing.T) {
			// Create a room
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset":       "public_chat",
				"room_version": "10",
			})

			// Add some push rules
			alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "pushrules", "global", testCase.ruleset, roomID},
				client.WithJSONBody(t, testCase.pushRuleJsonBuilder(roomID)),
			)

			// Sanity check the push rules are in the expected state before the upgrade
			pushRulesBefore := alice.GetAllPushRules(t)
			must.MatchGJSON(t, pushRulesBefore,
				match.JSONCheckOff(fmt.Sprintf("global.%s", testCase.ruleset),
					[]interface{}{
						roomID,
					},
					match.CheckOffMapper(func(r gjson.Result) interface{} { return r.Get("rule_id").Str }),
					match.CheckOffForEach(func(roomIDFromPushRule interface{}, result gjson.Result) error {
						for _, matcher := range testCase.pushRuleMatchersBuilder(roomIDFromPushRule.(string)) {
							if err := matcher(result); err != nil {
								return err
							}
						}
						return nil
					}),
				),
			)

			// Upgrade the room
			upgradeRes := alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "upgrade"},
				client.WithJSONBody(t, map[string]string{
					"new_version": "11",
				}),
			)
			body := must.ParseJSON(t, upgradeRes.Body)
			newRoomID := must.GetJSONFieldStr(t, body, "replacement_room")

			// Sanity check the push rules are in the expected state before the upgrade
			pushRulesAfter := alice.GetAllPushRules(t)
			must.MatchGJSON(t, pushRulesAfter,
				match.JSONCheckOff(fmt.Sprintf("global.%s", testCase.ruleset),
					[]interface{}{
						roomID,
						newRoomID,
					},
					match.CheckOffMapper(func(r gjson.Result) interface{} { return r.Get("rule_id").Str }),
					match.CheckOffForEach(func(roomIDFromPushRule interface{}, result gjson.Result) error {
						for _, matcher := range testCase.pushRuleMatchersBuilder(roomIDFromPushRule.(string)) {
							if err := matcher(result); err != nil {
								return err
							}
						}
						return nil
					}),
				),
			)
		})

		// When a homeserver becomes aware of a room upgrade (upgrade is done on remote
		// homeserver), it should copy over any push rules for all of its local users from the
		// old room to the new room.
		t.Run("joining a remote upgraded room carries over the push rules", func(t *testing.T) {
			// Alice create a room
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset":       "public_chat",
				"room_version": "10",
			})
			// Remote bob joins the room
			// alice.MustInviteRoom(t, roomID, bob.UserID)
			bob.MustJoinRoom(t, roomID, []spec.ServerName{
				deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
			})

			// Bob adds some push rules
			bob.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "pushrules", "global", "room", roomID},
				client.WithJSONBody(t, map[string]interface{}{
					"actions": []string{"dont_notify"},
				}),
			)

			// Sanity check the push rules are in the expected state before the upgrade
			pushRulesBefore := bob.GetAllPushRules(t)
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

			// Upgrade the room
			upgradeRes := alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "upgrade"},
				client.WithJSONBody(t, map[string]string{
					"new_version": "11",
				}),
			)
			body := must.ParseJSON(t, upgradeRes.Body)
			newRoomID := must.GetJSONFieldStr(t, body, "replacement_room")

			// Sanity check the push rules are in the expected state before the upgrade
			pushRulesAfter := bob.GetAllPushRules(t)
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
		})
	}

}
