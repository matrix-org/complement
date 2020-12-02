// +build msc2403

// This file contains tests for knocking, a currently experimental feature defined by MSC2403,
// which you can read here: https://github.com/matrix-org/matrix-doc/pull/2403

// Once this feature is included in a released version of the Matrix specification, the following
// will need to be carried out for these tests:
// * Update instances of `xyz.amorgan.knock` to `knock`.
// * Update endpoints from `/_matrix/client/unstable/xyz.amorgan.knock/...` to `/_matrix/client/rX/...`.
// * Remove the `build ...` line at the top so the tests are built by default.
// * Update the name of the test file to `knock_test.go` or similar.
// * Remove the conditional on rescinding knocks over federation. Synapse will need to support that if the spec says it's possible.

package tests

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
)

// A reason to include in the request body when testing knock reason parameters
const testKnockReason string = "Let me in... LET ME IN!!!"

// The unstable identifier to use while this feature is still unstable
const knockUnstableIdentifier string = "xyz.amorgan.knock"

// TestKnocking tests sending knock membership events and transitioning from knock to other membership states.
// Knocking is currently an experimental feature and not in the matrix spec.
// This function tests knocking on local and remote room.
func TestKnocking(t *testing.T) {
	deployment := Deploy(t, "test_knocking", b.BlueprintFederationTwoLocalOneRemote)
	defer deployment.Destroy(t)

	// Create a client for one local user
	aliceUserID := "@alice:hs1"
	alice := deployment.Client(t, "hs1", aliceUserID)

	// Create a client for another local user
	bobUserID := "@bob:hs1"
	bob := deployment.Client(t, "hs1", bobUserID)

	// Create a client for a remote user
	charlieUserID := "@charlie:hs2"
	charlie := deployment.Client(t, "hs2", charlieUserID)

	// Create a room for alice and bob to test knocking with
	roomIDOne := alice.CreateRoom(t, struct {
		Preset      string `json:"preset"`
		RoomVersion string `json:"room_version"`
	}{
		"private_chat",          // Set to private in order to get an invite-only room
		knockUnstableIdentifier, // Room version required for knocking. TODO: Remove when knocking is in a stable room version
	})

	// Test knocking between two users on the same homeserver
	knockingBetweenTwoUsersTest(t, roomIDOne, alice, bob, false)

	// Create a room for alice and charlie to test knocking with
	roomIDTwo := alice.CreateRoom(t, struct {
		Preset      string `json:"preset"`
		RoomVersion string `json:"room_version"`
	}{
		"private_chat",          // Set to private in order to get an invite-only room
		knockUnstableIdentifier, // Room version required for knocking. TODO: Remove when knocking is in a stable room version
	})

	// Test knocking between two users, each on a separate homeserver
	knockingBetweenTwoUsersTest(t, roomIDTwo, alice, charlie, true)
}

func knockingBetweenTwoUsersTest(t *testing.T, roomID string, inRoomUser, knockingUser *client.CSAPI, federation bool) {
	t.Run("Knocking on a room with a join rule other than '"+knockUnstableIdentifier+"' should fail", func(t *testing.T) {
		knockOnRoomWithStatus(t, knockingUser, roomID, "Can I knock anyways?", []string{"hs1"}, 403)
	})

	t.Run("Change the join rule of a room from 'invite' to '"+knockUnstableIdentifier+"'", func(t *testing.T) {
		emptyStateKey := ""
		inRoomUser.SendEventSynced(t, roomID, b.Event{
			Type:     "m.room.join_rules",
			Sender:   inRoomUser.UserID,
			StateKey: &emptyStateKey,
			Content: map[string]interface{}{
				"join_rule": knockUnstableIdentifier,
			},
		})
	})

	t.Run("Attempting to join a room with join rule '"+knockUnstableIdentifier+"' without an invite should fail", func(t *testing.T) {
		// Set server_name so we can find rooms via ID over federation
		query := url.Values{
			"server_name": []string{"hs1"},
		}

		knockingUser.MustDoWithStatusRaw(
			t,
			"POST",
			[]string{"_matrix", "client", "r0", "join", roomID},
			[]byte("{}"),
			"application/json",
			query,
			403,
		)
	})

	t.Run("Knocking on a room with join rule '"+knockUnstableIdentifier+"' should succeed", func(t *testing.T) {
		knockOnRoomSynced(t, knockingUser, roomID, testKnockReason, []string{"hs1"})
	})

	t.Run("A user that has already knocked cannot immediately knock again on the same room", func(t *testing.T) {
		knockOnRoomWithStatus(t, knockingUser, roomID, "I really like knock knock jokes", []string{"hs1"}, 403)
	})

	t.Run("Users in the room see a user's membership update when they knock", func(t *testing.T) {
		inRoomUser.SyncUntilTimelineHas(t, roomID, func(ev gjson.Result) bool {
			if ev.Get("type").Str != "m.room.member" || ev.Get("sender").Str != knockingUser.UserID {
				return false
			}
			must.EqualStr(t, ev.Get("content").Get("reason").Str, testKnockReason, "incorrect reason for knock")
			must.EqualStr(t, ev.Get("content").Get("membership").Str, knockUnstableIdentifier, "incorrect membership for knocking user")
			return true
		})
	})

	if !federation {
		// Rescinding a knock over federation is currently not supported in Synapse
		// TODO: Preventing another homeserver from running this test just because Synapse cannot is unfortunate
		t.Run("A user that has knocked on a local room can rescind their knock and then knock again", func(t *testing.T) {
			// We need to carry out an incremental sync after knocking in order to get leave information
			// Carry out an initial sync here and save the since token
			since := doInitialSync(t, knockingUser)

			// Rescind knock
			knockingUser.MustDo(
				t,
				"POST",
				[]string{"_matrix", "client", "r0", "rooms", roomID, "leave"},
				struct {
					Reason string `json:"reason"`
				}{
					"Just kidding!",
				},
			)

			// Use our sync token from earlier to carry out an incremental sync. Initial syncs may not contain room
			// leave information for obvious reasons
			knockingUser.SyncUntil(
				t,
				since,
				"rooms.leave."+client.GjsonEscape(roomID)+".timeline.events",
				func(ev gjson.Result) bool {
					if ev.Get("type").Str != "m.room.member" || ev.Get("sender").Str != knockingUser.UserID {
						return false
					}
					must.EqualStr(t, ev.Get("content").Get("membership").Str, "leave", "expected leave membership after rescinding a knock")
					return true
				},
			)

			// Knock again to return us to the knocked state
			knockOnRoomSynced(t, knockingUser, roomID, "Let me in... again?", []string{"hs1"})
		})
	}

	t.Run("A user in the room can reject a knock", func(t *testing.T) {
		// Reject the knock. Note that the knocking homeserver will *not* receive the leave
		// event over federation due to event validation complications.
		//
		// In the case of federation, this test will still check that a knock can be
		// carried out after a previous knock is rejected.
		inRoomUser.MustDo(
			t,
			"POST",
			[]string{"_matrix", "client", "r0", "rooms", roomID, "kick"},
			struct {
				UserID string `json:"user_id"`
				Reason string `json:"reason"`
			}{
				knockingUser.UserID,
				"I don't think so",
			},
		)

		// Wait until the leave membership event has come down sync
		inRoomUser.SyncUntilTimelineHas(t, roomID, func(ev gjson.Result) bool {
			return ev.Get("type").Str != "m.room.member" ||
				ev.Get("state_key").Str != knockingUser.UserID ||
				ev.Get("content").Get("membership").Str != "leave"
		})

		// Knock again
		knockOnRoomSynced(t, knockingUser, roomID, "Pleeease let me in?", []string{"hs1"})
	})

	t.Run("A user can knock on a room without a reason", func(t *testing.T) {
		// Reject the knock
		inRoomUser.MustDo(
			t,
			"POST",
			[]string{"_matrix", "client", "r0", "rooms", roomID, "kick"},
			struct {
				UserID string `json:"user_id"`
				Reason string `json:"reason"`
			}{
				knockingUser.UserID,
				"Please try again",
			},
		)

		// Knock again, this time without a reason
		knockOnRoomSynced(t, knockingUser, roomID, "", []string{"hs1"})
	})

	t.Run("A user in the room can accept a knock", func(t *testing.T) {
		inRoomUser.MustDo(
			t,
			"POST",
			[]string{"_matrix", "client", "r0", "rooms", roomID, "invite"},
			struct {
				UserID string `json:"user_id"`
				Reason string `json:"reason"`
			}{
				knockingUser.UserID,
				"Seems like a trustworthy fellow",
			},
		)

		// Wait until the invite membership event has come down sync
		inRoomUser.SyncUntilTimelineHas(t, roomID, func(ev gjson.Result) bool {
			return ev.Get("type").Str != "m.room.member" ||
				ev.Get("state_key").Str != knockingUser.UserID ||
				ev.Get("content").Get("membership").Str != "invite"
		})
	})

	t.Run("A user cannot knock on a room they are already in", func(t *testing.T) {
		reason := "I'm sticking my hand out the window and knocking again!"
		knockOnRoomWithStatus(t, knockingUser, roomID, reason, []string{"hs1"}, 403)
	})

	t.Run("A user that is banned from a room cannot knock on it", func(t *testing.T) {
		// Ban the user. Note that the knocking homeserver will *not* receive the ban
		// event over federation due to event validation complications.
		//
		// In the case of federation, this test will still check that a knock can not be
		// carried out after a ban.
		inRoomUser.MustDo(
			t,
			"POST",
			[]string{"_matrix", "client", "r0", "rooms", roomID, "ban"},
			struct {
				UserID string `json:"user_id"`
				Reason string `json:"reason"`
			}{
				knockingUser.UserID,
				"Turns out Bob wasn't that trustworthy after all!",
			},
		)

		// Wait until the ban membership event has come down sync
		inRoomUser.SyncUntilTimelineHas(t, roomID, func(ev gjson.Result) bool {
			return ev.Get("type").Str != "m.room.member" ||
				ev.Get("state_key").Str != knockingUser.UserID ||
				ev.Get("content").Get("membership").Str != "ban"
		})

		knockOnRoomWithStatus(t, knockingUser, roomID, "I didn't mean it!", []string{"hs1"}, 403)
	})
}

// knockOnRoomSynced will knock on a given room on the behalf of a user, and block until the knock has persisted.
// serverNames should be populated if knocking on a room that the user's homeserver isn't currently a part of.
// Fails the test if the knock response does not return a 200 status code.
func knockOnRoomSynced(t *testing.T, c *client.CSAPI, roomID, reason string, serverNames []string) {
	knockOnRoomWithStatus(t, c, roomID, reason, serverNames, 200)

	// The knock should have succeeded. Block until we see the knock appear down sync
	c.SyncUntil(
		t,
		"",
		"rooms."+client.GjsonEscape(knockUnstableIdentifier)+"."+client.GjsonEscape(roomID)+".knock_state.events",
		func(ev gjson.Result) bool {
			// We don't currently define any required state event types to be sent.
			// If we've reached this point, then an entry for this room was found
			return true
		},
	)
}

// knockOnRoomWithStatus will knock on a given room on the behalf of a user.
// serverNames should be populated if knocking on a room that the user's homeserver isn't currently a part of.
// expectedStatus allows setting an expected status code. If the response code differs, the test will fail.
func knockOnRoomWithStatus(t *testing.T, c *client.CSAPI, roomID, reason string, serverNames []string, expectedStatus int) {
	b := []byte("{}")
	var err error
	if reason != "" {
		// Add the reason to the request body
		requestBody := struct {
			Reason string `json:"reason"`
		}{
			// We specify a reason here instead of using the same one each time as implementations can
			// cache responses to identical requests
			reason,
		}
		b, err = json.Marshal(requestBody)
		must.NotError(t, "knockOnRoomWithStatus failed to marshal JSON body", err)
	}

	// Add any server names to the query parameters
	query := url.Values{
		"server_name": serverNames,
	}

	// Knock on the room
	c.MustDoWithStatusRaw(
		t,
		"POST",
		[]string{"_matrix", "client", "unstable", knockUnstableIdentifier, roomID},
		b,
		"application/json",
		query,
		expectedStatus,
	)

}

// doInitialSync will carry out an initial sync and return the next_batch token
func doInitialSync(t *testing.T, c *client.CSAPI) string {
	query := url.Values{
		"access_token": []string{c.AccessToken},
		"timeout":      []string{"1000"},
	}
	res, err := c.Do(t, "GET", []string{"_matrix", "client", "r0", "sync"}, nil, query)
	must.NotError(t, "doInitialSync failed to marshal JSON body", err)

	body := client.ParseJSON(t, res)
	since := client.GetJSONFieldStr(t, body, "next_batch")
	return since
}
