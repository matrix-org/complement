package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/tidwall/gjson"
)

func TestPushRuleRoomUpgrade(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
	})
	alice2 := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice2",
	})
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{
		LocalpartSuffix: "bob",
	})
	bob2 := deployment.Register(t, "hs2", helpers.RegistrationOpts{
		LocalpartSuffix: "bob2",
	})

	t.Run("parallel", func(t *testing.T) {
		for _, useManualRoomUpgrade := range []bool{true, false} {
			upgradeDescritorPrefix := ""
			if useManualRoomUpgrade {
				upgradeDescritorPrefix = "manually "
			}

			// When a homeserver becomes aware of a room upgrade (upgrade is done on local
			// homeserver), it should copy over any existing push rules for all of its local users
			// from the old room to the new room at the time of upgrade.
			t.Run(upgradeDescritorPrefix+"upgrading a room carries over existing push rules for local users", func(t *testing.T) {
				t.Parallel()

				// FIXME: We have to skip this test on Synapse until
				// https://github.com/element-hq/synapse/issues/19199 is resolved.
				if useManualRoomUpgrade {
					runtime.SkipIf(t, runtime.Synapse)
				}

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
					t.Logf("Checking push rules (before upgrade) for %s", client.UserID)
					pushRulesBefore := client.GetAllPushRules(t)
					must.MatchGJSON(t, pushRulesBefore,
						match.JSONCheckOff("global.room",
							[]interface{}{
								roomID,
							},
							match.CheckOffAllowUnwanted(),
							match.CheckOffMapper(func(r gjson.Result) interface{} { return r.Get("rule_id").Str }),
							match.CheckOffForEach(func(roomIDFromPushRule interface{}, result gjson.Result) error {
								return match.JSONKeyEqual("actions.0", "dont_notify")(result)
							}),
						),
					)
				}

				// Upgrade the room
				var newRoomID string
				if useManualRoomUpgrade {
					newRoomID = mustManualUpgradeRoom(t, alice, roomID, "11")
				} else {
					newRoomID = alice.MustUpgradeRoom(t, roomID, "11")
				}
				t.Logf("Upgraded room %s to %s", roomID, newRoomID)

				// Alice2 joins the new room
				alice2.MustJoinRoom(t, newRoomID, nil)

				// Sanity check the push rules are in the expected state after the upgrade
				for _, client := range []*client.CSAPI{alice, alice2} {
					t.Logf("Checking push rules (after upgrade) for %s", client.UserID)
					pushRulesAfter := client.GetAllPushRules(t)
					must.MatchGJSON(t, pushRulesAfter,
						match.JSONCheckOff("global.room",
							[]interface{}{
								roomID,
								newRoomID,
							},
							match.CheckOffAllowUnwanted(),
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
			t.Run("joining a remote "+upgradeDescritorPrefix+"upgraded room carries over existing push rules", func(t *testing.T) {
				t.Parallel()

				// Start a sync loop
				_, bobSince := bob.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

				// Alice create a room
				roomID := alice.MustCreateRoom(t, map[string]interface{}{
					"preset":       "public_chat",
					"room_version": "10",
				})
				// Remote bob joins the room
				bob.MustJoinRoom(t, roomID, []spec.ServerName{
					deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
				})
				// Wait until the homeserver is fully participating in the room so that we can
				// double-check the subsequent joins also work (sanity check participating vs
				// non-participating logic in the homeserver)
				bob.MustAwaitPartialStateJoinCompletion(t, roomID)
				// Wait until we know the first bob is joined for sure. We want to make sure bob2
				// doesn't also race us to remotely join the room as bob2 should be able to
				// locally join and then send a join over federation (because the first bob is
				// already joined to the room).
				bobSince = bob.MustSyncUntil(t, client.SyncReq{Since: bobSince}, client.SyncJoinedTo(bob.UserID, roomID))
				// Remote bob2 joins the room
				//
				// We use two users to ensure that all users get taken care of (not just the first
				// user on the homeserver).
				bob2.MustJoinRoom(t, roomID,
					// bob2 can do a local join since bob is already in the room. No need to specify
					// via servers here.
					//
					// []spec.ServerName{
					// 	deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
					// },
					nil,
				)

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
					t.Logf("Checking push rules (before upgrade) for %s", client.UserID)
					pushRulesBefore := client.GetAllPushRules(t)
					must.MatchGJSON(t, pushRulesBefore,
						match.JSONCheckOff("global.room",
							[]interface{}{
								roomID,
							},
							match.CheckOffAllowUnwanted(),
							match.CheckOffMapper(func(r gjson.Result) interface{} { return r.Get("rule_id").Str }),
							match.CheckOffForEach(func(roomIDFromPushRule interface{}, result gjson.Result) error {
								return match.JSONKeyEqual("actions.0", "dont_notify")(result)
							}),
						),
					)
				}

				// Upgrade the room
				var newRoomID string
				if useManualRoomUpgrade {
					newRoomID = mustManualUpgradeRoom(t, alice, roomID, "11")
				} else {
					newRoomID = alice.MustUpgradeRoom(t, roomID, "11")
				}
				t.Logf("Upgraded room %s to %s", roomID, newRoomID)

				// Ensure that the remote server sees the tombstone in the old room before
				// joining the new room (avoid races and the client woudn't know where to go
				// without this hint anyway)
				bobSince = bob.MustSyncUntil(t, client.SyncReq{Since: bobSince}, client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
					return ev.Get("type").Str == "m.room.tombstone" && ev.Get("state_key").Str == ""
				}))

				// Remote bob joins the new room
				bob.MustJoinRoom(t, newRoomID, []spec.ServerName{
					deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
				})
				// Wait until the homeserver is fully participating in the room so that we can
				// double-check the subsequent joins also work (sanity check participating vs
				// non-participating logic in the homeserver)
				bob.MustAwaitPartialStateJoinCompletion(t, newRoomID)
				// Wait until we know the first bob is joined for sure. We want to make sure bob2
				// doesn't also race us to remotely join the room as bob2 should be able to
				// locally join and then send a join over federation (because the first bob is
				// already joined to the room).
				bobSince = bob.MustSyncUntil(t, client.SyncReq{Since: bobSince}, client.SyncJoinedTo(bob.UserID, newRoomID))
				// Remote bob2 joins the new room
				bob2.MustJoinRoom(t, newRoomID,
					// bob2 can do a local join since bob is already in the room. No need to specify
					// via servers here.
					//
					// []spec.ServerName{
					// 	deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
					// },
					nil,
				)

				// Sanity check the push rules are in the expected state after the upgrade
				for _, client := range []*client.CSAPI{bob, bob2} {
					pushRulesAfter := client.GetAllPushRules(t)
					t.Logf("Checking push rules (after upgrade) for %s", client.UserID)
					must.MatchGJSON(t, pushRulesAfter,
						match.JSONCheckOff("global.room",
							[]interface{}{
								roomID,
								newRoomID,
							},
							match.CheckOffAllowUnwanted(),
							match.CheckOffMapper(func(r gjson.Result) interface{} { return r.Get("rule_id").Str }),
							match.CheckOffForEach(func(roomIDFromPushRule interface{}, result gjson.Result) error {
								return match.JSONKeyEqual("actions.0", "dont_notify")(result)
							}),
						),
					)
				}
			})
		}
	})
}

func mustManualUpgradeRoom(t *testing.T, c *client.CSAPI, oldRoomID string, newRoomVersion string) string {
	t.Helper()

	// Create a new room
	newRoomID := c.MustCreateRoom(t, map[string]interface{}{
		"preset":       "public_chat",
		"room_version": newRoomVersion,
		"creation_content": map[string]interface{}{
			// Specify the old room as the predecessor
			"predecessor": map[string]interface{}{
				"room_id": oldRoomID,
			},
		},
	})

	// Send the m.room.tombstone event to the old room
	c.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", oldRoomID, "state", "m.room.tombstone", ""}, client.WithJSONBody(t, map[string]interface{}{
		"body":             "This room has been replaced",
		"replacement_room": newRoomID,
	}))

	return newRoomID
}
