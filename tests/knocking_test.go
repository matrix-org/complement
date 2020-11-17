// +build knock

package tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
)

// TestKnockingLocal tests that a user knocking on a room which the homeserver is already a part of works
func TestKnockingLocal(t *testing.T) {
	deployment := Deploy(t, "local_knocking", b.BlueprintAliceBob)
	defer deployment.Destroy(t)

	aliceUserID := "@alice:hs1"
	alice := deployment.Client(t, "hs1", aliceUserID)
	roomID := alice.CreateRoom(t, struct {
		Preset      string `json:"preset"`
		RoomVersion string `json:"room_version"`
		Name        string `json:"name"`
		Topic       string `json:"topic"`
	}{
		"private_chat",      // Set to private in order to get an invite-only room
		"xyz.amorgan.knock", // Room version required for knocking. TODO: Remove when knocking is in a stable room version
		// Add some state to the room. We'll later check that this comes down sync correctly.
		"knocking test room",
		"Who's there?",
	})

	bobUserID := "@bob:hs1"
	bob := deployment.Client(t, "hs1", bobUserID)
	knockReason := "Let me in... LET ME IN!!!"

	t.Run("Knocking on a room with a join rule other than 'knock' should fail", func(t *testing.T) {
		bob.MustDoWithStatus(
			t,
			"POST",
			[]string{"_matrix", "client", "unstable", "xyz.amorgan.knock", roomID},
			struct {
				Reason string `json:"reason"`
			}{
				knockReason,
			},
			403,
		)
	})

	t.Run("Change the join rule of a room from 'invite' to 'xyz.amorgan.knock'", func(t *testing.T) {
		alice.MustDo(
			t,
			"PUT",
			[]string{"_matrix", "client", "r0", "rooms", roomID, "state", "m.room.join_rules", ""},
			struct {
				JoinRule string `json:"join_rule"`
			}{
				"xyz.amorgan.knock",
			},
		)
	})

	t.Run("Attempting to join a room with join rule 'xyz.amorgan.knock' without an invite should fail", func(t *testing.T) {
		bob.MustDoWithStatus(
			t,
			"POST",
			[]string{"_matrix", "client", "r0", "join", roomID},
			struct{}{},
			403,
		)
	})

	t.Run("Knocking on a room with join rule 'xyz.amorgan.xyz' should succeed", func(t *testing.T) {
		bob.MustDo(
			t,
			"POST",
			[]string{"_matrix", "client", "unstable", "xyz.amorgan.knock", roomID},
			struct {
				Reason string `json:"reason"`
			}{
				knockReason,
			},
		)
	})

	t.Run("A user that has already knocked cannot immediately knock again on the same room", func(t *testing.T) {
		bob.MustDoWithStatus(
			t,
			"POST",
			[]string{"_matrix", "client", "unstable", "xyz.amorgan.knock", roomID},
			struct {
				Reason string `json:"reason"`
			}{
				"Let me in... again?",
			},
			403,
		)
	})

	// Now that Bob's knocked on the room successfully, let's check that everything went right

	t.Run("parallel", func(t *testing.T) {
		t.Run("Users in the room see a user's membership update when they knock", func(t *testing.T) {
			t.Parallel()
			alice.SyncUntilTimelineHas(t, roomID, func(ev gjson.Result) bool {
				if ev.Get("type").Str != "m.room.member" || ev.Get("sender").Str != bobUserID {
					return false
				}
				must.EqualStr(t, ev.Get("content").Get("reason").Str, knockReason, "incorrect reason for knock")
				must.EqualStr(t, ev.Get("content").Get("membership").Str, "xyz.amorgan.knock", "incorrect membership for knocking user")
				return true
			})
		})

		t.Run("Users see state events from the room that they knocked on in /sync", func(t *testing.T) {
			t.Parallel()
			bob.SyncUntilKnockHas(t, roomID, func(ev gjson.Result) bool {
				// We don't current define any required state event types to be sent
				// If we've reached this point, then an entry for this room was found
				return true
			})
		})
	})

	t.Run("A user that has knocked on a room can rescind their knock and then knock again", func(t *testing.T) {
		// Rescind knock
		bob.MustDo(
			t,
			"POST",
			[]string{"_matrix", "client", "r0", "rooms", roomID, "leave"},
			struct {
				Reason string `json:"reason"`
			}{
				"Just kidding!",
			},
		)

		// Knock again
		bob.MustDo(
			t,
			"POST",
			[]string{"_matrix", "client", "unstable", "xyz.amorgan.knock", roomID},
			struct {
				Reason string `json:"reason"`
			}{
				"Let me in... again?",
			},
		)
	})

	t.Run("A user in the room can reject a knock", func(t *testing.T) {
		// Reject the knock
		alice.MustDo(
			t,
			"POST",
			[]string{"_matrix", "client", "r0", "rooms", roomID, "kick"},
			struct {
				UserID string `json:"user_id"`
				Reason string `json:"reason"`
			}{
				bobUserID,
				"I don't think so",
			},
		)

		// Knock again
		bob.MustDo(
			t,
			"POST",
			[]string{"_matrix", "client", "unstable", "xyz.amorgan.knock", roomID},
			struct {
				Reason string `json:"reason"`
			}{
				"Pleeeeease let me in?",
			},
		)
	})

	t.Run("A user in the room can accept a knock", func(t *testing.T) {
		alice.MustDo(
			t,
			"POST",
			[]string{"_matrix", "client", "r0", "rooms", roomID, "invite"},
			struct {
				UserID string `json:"user_id"`
				Reason string `json:"reason"`
			}{
				bobUserID,
				"Seems like a trustworthy fellow",
			},
		)
	})
}
