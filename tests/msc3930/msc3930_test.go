// This file contains tests for "push rules of polls" as defined by MSC3930.
// The MSC that defines the design of the polls system is MSC3381.
//
// Note that implementation of MSC3381 is not required by the homeserver to
// pass the tests in this file.
//
// You can read either MSC using the links below.
// Polls: https://github.com/matrix-org/matrix-doc/pull/3381
// Push rules for polls: https://github.com/matrix-org/matrix-doc/pull/3930

package tests

import (
	"math"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

const pollResponseRuleID = ".org.matrix.msc3930.rule.poll_response"
const pollStartOneToOneRuleID = ".org.matrix.msc3930.rule.poll_start_one_to_one"
const pollEndOneToOneRuleID = ".org.matrix.msc3930.rule.poll_end_one_to_one"
const pollStartRuleID = ".org.matrix.msc3930.rule.poll_start"
const pollEndRuleID = ".org.matrix.msc3930.rule.poll_end"

func TestPollsLocalPushRules(t *testing.T) {
	deployment := complement.Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	// Create a user to poll the push rules of.
	aliceUserID := "@alice:hs1"
	alice := deployment.Client(t, "hs1", aliceUserID)

	// Test for the presence of the expected push rules. Clients are expected
	// to implement local matching of events based on the presented rules.
	t.Run("Polls push rules are correctly presented to the client", func(t *testing.T) {
		// Request each of the push rule IDs defined by MSC3930 and verify their structure.

		// This push rule silences all poll responses.
		pollResponseRule := alice.GetPushRule(t, "global", "override", pollResponseRuleID)
		must.MatchGJSON(
			t,
			pollResponseRule,
			match.JSONKeyEqual("actions", []string{}),
			match.JSONKeyEqual("default", true),
			match.JSONKeyEqual("enabled", true),
			// There should only be one condition defined for this type
			match.JSONKeyEqual("conditions.#", 1),
			// Check the contents of the first (and only) condition
			match.JSONKeyEqual("conditions.0.kind", "event_match"),
			match.JSONKeyEqual("conditions.0.key", "type"),
			match.JSONKeyEqual("conditions.0.pattern", "org.matrix.msc3381.poll.response"),
		)

		// This push rule creates a sound and notifies the user when a poll is started in a one-to-one room.
		pollStartOneToOneRule := alice.GetPushRule(t, "global", "underride", pollStartOneToOneRuleID)
		must.MatchGJSON(
			t,
			pollStartOneToOneRule,
			// Check that the appropriate actions are set for this rule
			match.JSONKeyEqual("actions.#", 2),
			match.JSONKeyEqual("actions", []interface{}{
				"notify",
				map[string]interface{}{"set_tweak": "sound", "value": "default"},
			},
			),
			match.JSONKeyEqual("default", true),
			match.JSONKeyEqual("enabled", true),
			// There should two conditions defined for this type
			match.JSONKeyEqual("conditions.#", 2),
			// Check the condition that requires a room between two users
			match.JSONKeyEqual("conditions.#(kind==\"room_member_count\").is", "2"),
			// Check the condition that requires a poll start event
			match.JSONKeyEqual("conditions.#(kind==\"event_match\").key", "type"),
			match.JSONKeyEqual("conditions.#(kind==\"event_match\").pattern", "org.matrix.msc3381.poll.start"),
		)

		// This push rule creates a sound and notifies the user when a poll is ended in a one-to-one room.
		pollEndOneToOneRule := alice.GetPushRule(t, "global", "underride", pollEndOneToOneRuleID)
		must.MatchGJSON(
			t,
			pollEndOneToOneRule,
			// Check that the appropriate actions are set for this rule
			match.JSONKeyEqual("actions.#", 2),
			match.JSONKeyEqual("actions", []interface{}{
				"notify",
				map[string]interface{}{"set_tweak": "sound", "value": "default"},
			},
			),
			match.JSONKeyEqual("default", true),
			match.JSONKeyEqual("enabled", true),
			// There should two conditions defined for this type
			match.JSONKeyEqual("conditions.#", 2),
			// Check the condition that requires a room between two users
			match.JSONKeyEqual("conditions.#(kind==\"room_member_count\").is", "2"),
			// Check the condition that requires a poll start event
			match.JSONKeyEqual("conditions.#(kind==\"event_match\").key", "type"),
			match.JSONKeyEqual("conditions.#(kind==\"event_match\").pattern", "org.matrix.msc3381.poll.end"),
		)

		// This push rule notifies the user when a poll is started in any room.
		pollStartRule := alice.GetPushRule(t, "global", "underride", pollStartRuleID)
		must.MatchGJSON(
			t,
			pollStartRule,
			// Check that the appropriate actions are set for this rule
			match.JSONKeyEqual("actions", []string{"notify"}),
			match.JSONKeyEqual("default", true),
			match.JSONKeyEqual("enabled", true),
			// There should only be one condition defined for this type
			match.JSONKeyEqual("conditions.#", 1),
			// Check the contents of the first (and only) condition
			match.JSONKeyEqual("conditions.0.kind", "event_match"),
			match.JSONKeyEqual("conditions.0.key", "type"),
			match.JSONKeyEqual("conditions.0.pattern", "org.matrix.msc3381.poll.start"),
		)

		// This push rule notifies the user when a poll is ended in any room.
		pollEndRule := alice.GetPushRule(t, "global", "underride", pollEndRuleID)
		must.MatchGJSON(
			t,
			pollEndRule,
			// Check that the appropriate actions are set for this rule
			match.JSONKeyEqual("actions", []string{"notify"}),
			match.JSONKeyEqual("default", true),
			match.JSONKeyEqual("enabled", true),
			// There should only be one condition defined for this type
			match.JSONKeyEqual("conditions.#", 1),
			// Check the contents of the first (and only) condition
			match.JSONKeyEqual("conditions.0.kind", "event_match"),
			match.JSONKeyEqual("conditions.0.key", "type"),
			match.JSONKeyEqual("conditions.0.pattern", "org.matrix.msc3381.poll.end"),
		)

		// The DM-specific rules for poll start and poll end should come before the rules that
		// define behaviour for any room. We verify this by ensuring that the DM-specific rules
		// have a lower index when requesting all push rules.
		allPushRules := alice.GetAllPushRules(t)
		globalUnderridePushRules := allPushRules.Get("global").Get("underride").Array()

		pollStartOneToOneRuleIndex := math.MaxInt64
		pollEndOneToOneRuleIndex := math.MaxInt64
		for index, rule := range globalUnderridePushRules {
			// Iterate over the user's global underride rules as a client would.
			// If we come across a one-to-one room rule ID, we note down its index.
			// When we come across the rule ID of its generic counterpart, we ensure
			// its counterpart is at a higher index, and thus would be considered
			// lower priority.
			if rule.Get("rule_id").Str == pollStartOneToOneRuleID {
				pollStartOneToOneRuleIndex = index
			} else if rule.Get("rule_id").Str == pollEndOneToOneRuleID {
				pollEndOneToOneRuleIndex = index
			} else if rule.Get("rule_id").Str == pollStartRuleID {
				if pollStartOneToOneRuleIndex > index {
					t.Fatalf(
						"Expected rule '%s' to come after '%s' in '%s's global underride rules",
						pollStartRuleID,
						pollStartOneToOneRuleID,
						alice.UserID,
					)
				}
			} else if rule.Get("rule_id").Str == pollEndRuleID {
				if pollEndOneToOneRuleIndex > index {
					t.Fatalf(
						"Expected rule '%s' to come after '%s' in '%s's global underride rules",
						pollEndRuleID,
						pollEndOneToOneRuleID,
						alice.UserID,
					)
				}
			}
		}
	})

	// TODO: Test whether the homeserver correctly calls POST /_matrix/push/v1/notify on the push gateway
	// in accordance with the push rules. Blocked by Complement not having a push gateway implementation.
}
