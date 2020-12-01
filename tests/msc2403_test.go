// +build msc2403

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

// A reason to include in the response body while knocking
var testKnockReason string = "Let me in... LET ME IN!!!"

// TestKnocking tests that a user knocking on a room which the homeserver is already a part of works
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
		"private_chat",      // Set to private in order to get an invite-only room
		"xyz.amorgan.knock", // Room version required for knocking. TODO: Remove when knocking is in a stable room version
	})

	// Test knocking between two users on the same homeserver
	knockingBetweenTwoUsersTest(t, roomIDOne, alice, bob, false)

	// Create a room for alice and remoteAlice to test knocking with
	roomIDTwo := alice.CreateRoom(t, struct {
		Preset      string `json:"preset"`
		RoomVersion string `json:"room_version"`
	}{
		"private_chat",      // Set to private in order to get an invite-only room
		"xyz.amorgan.knock", // Room version required for knocking. TODO: Remove when knocking is in a stable room version
	})

	// Test knocking between two users, each on a separate homeserver
	knockingBetweenTwoUsersTest(t, roomIDTwo, alice, charlie, true)
}

func knockingBetweenTwoUsersTest(t *testing.T, roomID string, inRoomUser, knockingUser *client.CSAPI, federation bool) {
	knockingUserID := knockingUser.UserID

	t.Run("Knocking on a room with a join rule other than 'knock' should fail", func(t *testing.T) {
		knockOnRoomWithStatus(t, knockingUser, roomID, "Can I knock anyways?", []string{"hs1"}, 403)
	})

	t.Run("Change the join rule of a room from 'invite' to 'xyz.amorgan.knock'", func(t *testing.T) {
		inRoomUser.MustDo(
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

	t.Run("Knocking on a room with join rule 'xyz.amorgan.xyz' should succeed", func(t *testing.T) {
		knockOnRoom(t, knockingUser, roomID, testKnockReason, []string{"hs1"})
	})

	t.Run("A user that has already knocked cannot immediately knock again on the same room", func(t *testing.T) {
		knockOnRoomWithStatus(t, knockingUser, roomID, "I really like knock knock jokes", []string{"hs1"}, 403)
	})

	// Now that Bob's knocked on the room successfully, let's check that everything went right

	t.Run("parallel", func(t *testing.T) {
		t.Run("Users in the room see a user's membership update when they knock", func(t *testing.T) {
			t.Parallel()
			inRoomUser.SyncUntilTimelineHas(t, roomID, func(ev gjson.Result) bool {
				if ev.Get("type").Str != "m.room.member" || ev.Get("sender").Str != knockingUserID {
					return false
				}
				must.EqualStr(t, ev.Get("content").Get("reason").Str, testKnockReason, "incorrect reason for knock")
				must.EqualStr(t, ev.Get("content").Get("membership").Str, "xyz.amorgan.knock", "incorrect membership for knocking user")
				return true
			})
		})

		t.Run("Users see state events from the room that they knocked on in /sync", func(t *testing.T) {
			t.Parallel()
			knockingUser.SyncUntil(
				t,
				"",
				"rooms."+client.GjsonEscape("xyz.amorgan.knock")+"."+client.GjsonEscape(roomID)+".knock_state.events",
				func(ev gjson.Result) bool {
					// We don't current define any required state event types to be sent
					// If we've reached this point, then an entry for this room was found
					return true
				},
			)
		})
	})

	if !federation {
		// Rescinding a knock over federation is currently not supported in Synapse
		//
		t.Run("A user that has knocked on a local room can rescind their knock and then knock again", func(t *testing.T) {
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

			// Knock again
			knockOnRoom(t, knockingUser, roomID, "Let me in... again?", []string{"hs1"})
		})
	}

	t.Run("A user in the room can reject a knock", func(t *testing.T) {
		// Reject the knock
		inRoomUser.MustDo(
			t,
			"POST",
			[]string{"_matrix", "client", "r0", "rooms", roomID, "kick"},
			struct {
				UserID string `json:"user_id"`
				Reason string `json:"reason"`
			}{
				knockingUserID,
				"I don't think so",
			},
		)

		// Knock again
		knockOnRoom(t, knockingUser, roomID, "Pleeease let me in?", []string{"hs1"})
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
				knockingUserID,
				"Please try again",
			},
		)

		// Knock again, this time without a reason
		knockingUser.MustDoRaw(
			t,
			"POST",
			[]string{"_matrix", "client", "unstable", "xyz.amorgan.knock", roomID},
			[]byte("{}"),
			"application/json",
			url.Values{"server_name": []string{"hs1"}},
		)
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
				knockingUserID,
				"Seems like a trustworthy fellow",
			},
		)
	})

	t.Run("A user cannot knock on a room they are already in", func(t *testing.T) {
		reason := "I'm sticking my hand out the window and knocking again!"
		knockOnRoomWithStatus(t, knockingUser, roomID, reason, []string{"hs1"}, 403)
	})

	t.Run("A user that is banned from a room cannot knock on it", func(t *testing.T) {
		inRoomUser.MustDo(
			t,
			"POST",
			[]string{"_matrix", "client", "r0", "rooms", roomID, "ban"},
			struct {
				UserID string `json:"user_id"`
				Reason string `json:"reason"`
			}{
				knockingUserID,
				"Turns out Bob wasn't that trustworthy after all!",
			},
		)

		knockOnRoomWithStatus(t, knockingUser, roomID, "I didn't mean it!", []string{"hs1"}, 403)
	})
}

func knockOnRoom(t *testing.T, client *client.CSAPI, roomID string, reason string, serverNames []string) {
	knockOnRoomWithStatus(t, client, roomID, reason, serverNames, 200)
}

func knockOnRoomWithStatus(t *testing.T, client *client.CSAPI, roomID string, reason string, serverNames []string, expectedStatus int) {
	// Add the reason to the request body
	requestBody := struct {
		Reason string `json:"reason"`
	}{
		// We specify a reason here instead of using the same one each time as implementations can
		// cache responses to identical requests
		reason,
	}
	b, err := json.Marshal(requestBody)
	if err != nil {
		t.Fatalf("knockOnRoomWithStatus failed to marshal JSON body: %s", err)
	}

	// Add any server names to the query parameters
	query := url.Values{
		"server_name": serverNames,
	}

	// Knock on the room
	client.MustDoWithStatusRaw(
		t,
		"POST",
		[]string{"_matrix", "client", "unstable", "xyz.amorgan.knock", roomID},
		b,
		"application/json",
		query,
		expectedStatus,
	)
}
