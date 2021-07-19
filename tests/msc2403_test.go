// +build msc2403

// This file contains tests for knocking, a currently experimental feature defined by MSC2403,
// which you can read here: https://github.com/matrix-org/matrix-doc/pull/2403

package tests

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/matrix-org/gomatrixserverlib"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

// A reason to include in the request body when testing knock reason parameters
const testKnockReason string = "Let me in... LET ME IN!!!"

// TestKnocking tests sending knock membership events and transitioning from knock to other membership states.
// Knocking is currently an experimental feature and not in the matrix spec.
// This function tests knocking on local and remote room.
func TestKnocking(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationTwoLocalOneRemote)
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

	// Create a server to observe
	inviteWaiter := NewWaiter()
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleInviteRequests(func(ev *gomatrixserverlib.Event) {
			inviteWaiter.Finish()
		}),
		federation.HandleTransactionRequests(nil, nil),
	)
	cancel := srv.Listen()
	defer cancel()
	srv.UnexpectedRequestsAreErrors = false
	david := srv.UserID("david")

	// Create a room for alice and bob to test knocking with
	roomIDOne := alice.CreateRoom(t, struct {
		Preset      string `json:"preset"`
		RoomVersion string `json:"room_version"`
	}{
		"private_chat", // Set to private in order to get an invite-only room
		"7",            // Room version required for knocking.
	})
	alice.InviteRoom(t, roomIDOne, david)
	inviteWaiter.Wait(t, 5*time.Second)
	serverRoomOne := srv.MustJoinRoom(t, deployment, "hs1", roomIDOne, david)

	// Test knocking between two users on the same homeserver
	knockingBetweenTwoUsersTest(t, roomIDOne, alice, bob, serverRoomOne, false)

	// Create a room for alice and charlie to test knocking with
	roomIDTwo := alice.CreateRoom(t, struct {
		Preset      string `json:"preset"`
		RoomVersion string `json:"room_version"`
	}{
		"private_chat", // Set to private in order to get an invite-only room
		"7",            // Room version required for knocking.
	})
	inviteWaiter = NewWaiter()
	alice.InviteRoom(t, roomIDTwo, david)
	inviteWaiter.Wait(t, 5*time.Second)
	serverRoomTwo := srv.MustJoinRoom(t, deployment, "hs1", roomIDTwo, david)

	// Test knocking between two users, each on a separate homeserver
	knockingBetweenTwoUsersTest(t, roomIDTwo, alice, charlie, serverRoomTwo, true)
}

func knockingBetweenTwoUsersTest(t *testing.T, roomID string, inRoomUser, knockingUser *client.CSAPI, serverRoom *federation.ServerRoom, testFederation bool) {
	t.Run("Knocking on a room with a join rule other than 'knock' should fail", func(t *testing.T) {
		knockOnRoomWithStatus(t, knockingUser, roomID, "Can I knock anyways?", []string{"hs1"}, 403)
	})

	t.Run("Change the join rule of a room from 'invite' to 'knock'", func(t *testing.T) {
		emptyStateKey := ""
		inRoomUser.SendEventSynced(t, roomID, b.Event{
			Type:     "m.room.join_rules",
			Sender:   inRoomUser.UserID,
			StateKey: &emptyStateKey,
			Content: map[string]interface{}{
				"join_rule": "knock",
			},
		})
	})

	t.Run("Attempting to join a room with join rule 'knock' without an invite should fail", func(t *testing.T) {
		// Set server_name so we can find rooms via ID over federation
		query := url.Values{
			"server_name": []string{"hs1"},
		}

		res := knockingUser.DoFunc(
			t,
			"POST",
			[]string{"_matrix", "client", "r0", "join", roomID},
			client.WithQueries(query),
			client.WithRawBody([]byte(`{}`)),
		)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 403,
		})
	})

	t.Run("Knocking on a room with join rule 'knock' should succeed", func(t *testing.T) {
		knockOnRoomSynced(t, knockingUser, roomID, testKnockReason, []string{"hs1"})
	})

	t.Run("A user that has already knocked is allowed to knock again on the same room", func(t *testing.T) {
		knockOnRoomSynced(t, knockingUser, roomID, "I really like knock knock jokes", []string{"hs1"})
	})

	t.Run("Users in the room see a user's membership update when they knock", func(t *testing.T) {
		// check the membership seen over the federation
		knockerState := serverRoom.CurrentState("m.room.member", knockingUser.UserID)
		if knockerState == nil {
			t.Errorf("Did not get membership state for knocking user")
		} else {
			m, err := knockerState.Membership()
			if err != nil {
				t.Errorf("Unable to unpack membership state for knocking user: %v", err)
			} else if m != "knock" {
				t.Errorf("membership for knocking user: got %#v, want \"knock\"", m)
			}
		}

		inRoomUser.SyncUntilTimelineHas(t, roomID, func(ev gjson.Result) bool {
			if ev.Get("type").Str != "m.room.member" || ev.Get("sender").Str != knockingUser.UserID {
				return false
			}
			must.EqualStr(t, ev.Get("content").Get("reason").Str, testKnockReason, "incorrect reason for knock")
			must.EqualStr(t, ev.Get("content").Get("membership").Str, "knock", "incorrect membership for knocking user")
			return true
		})
	})

	if !testFederation {
		// Rescinding a knock over federation is currently not specced
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
				"",
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
		"",
		"rooms.knock."+client.GjsonEscape(roomID)+".knock_state.events",
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
	res := c.DoFunc(
		t,
		"POST",
		[]string{"_matrix", "client", "r0", "knock", roomID},
		client.WithQueries(query),
		client.WithRawBody(b),
	)
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: expectedStatus,
	})
}

// doInitialSync will carry out an initial sync and return the next_batch token
func doInitialSync(t *testing.T, c *client.CSAPI) string {
	query := url.Values{
		"timeout": []string{"1000"},
	}
	res := c.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "sync"}, client.WithQueries(query))
	body := client.ParseJSON(t, res)
	since := client.GetJSONFieldStr(t, body, "next_batch")
	return since
}

// TestKnockRoomsInPublicRoomsDirectory will create a knock room, attempt to publish it to the public rooms directory,
// and then check that the room appears in the directory. The room's entry should also have a 'join_rule' field
// representing a knock room. For sanity-checking, this test will also create a public room and ensure it has a
// 'join_rule' representing a publicly-joinable room.
func TestKnockRoomsInPublicRoomsDirectory(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	// Create a client for a local user
	aliceUserID := "@alice:hs1"
	alice := deployment.Client(t, "hs1", aliceUserID)

	// Create an invite-only room with the knock room version
	roomID := alice.CreateRoom(t, struct {
		Preset      string `json:"preset"`
		RoomVersion string `json:"room_version"`
	}{
		"private_chat", // Set to private in order to get an invite-only room
		"7",            // Room version required for knocking.
	})

	// Change the join_rule to allow knocking
	emptyStateKey := ""
	alice.SendEventSynced(t, roomID, b.Event{
		Type:     "m.room.join_rules",
		Sender:   alice.UserID,
		StateKey: &emptyStateKey,
		Content: map[string]interface{}{
			"join_rule": "knock",
		},
	})

	// Publish the room to the public room directory and check that the 'join_rule' key is knock
	publishAndCheckRoomJoinRule(t, alice, roomID, "knock")

	// Create a public room
	roomID = alice.CreateRoom(t, struct {
		Preset string `json:"preset"`
	}{
		"public_chat", // Set to public in order to get a public room
	})

	// Publish the room, and check that the public room directory presents a 'join_rule' key of public
	publishAndCheckRoomJoinRule(t, alice, roomID, "public")
}

// publishAndCheckRoomJoinRule will publish a given room ID to the given user's public room directory.
// It will then query the directory and ensure the room is listed, and has a given 'join_rule' entry
func publishAndCheckRoomJoinRule(t *testing.T, c *client.CSAPI, roomID, expectedJoinRule string) {
	// Publish the room to the public room directory
	c.MustDo(
		t,
		"PUT",
		[]string{"_matrix", "client", "r0", "directory", "list", "room", roomID},
		struct {
			Visibility string `json:"visibility"`
		}{
			"public",
		},
	)

	// Check that we can see the room in the directory
	res := c.MustDo(
		t,
		"GET",
		[]string{"_matrix", "client", "r0", "publicRooms"},
		nil,
	)

	roomFound := false
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			// For each public room directory chunk (representing a single room entry)
			match.JSONArrayEach("chunk", func(r gjson.Result) error {
				// If this is our room
				if r.Get("room_id").Str == roomID {
					roomFound = true

					// Check that the join_rule key exists and is as we expect
					if roomJoinRule := r.Get("join_rule").Str; roomJoinRule != expectedJoinRule {
						return fmt.Errorf(
							"'join_rule' key for room in public room chunk is '%s', expected '%s'",
							roomJoinRule, expectedJoinRule,
						)
					}
				}
				return nil
			}),
		},
	})

	// Check that we did in fact see the room
	if !roomFound {
		t.Fatalf("Room was not present in public room directory response")
	}
}

// TestCannotSendNonKnockViaSendKnock checks that we cannot submit anything via /send_knock except a knock
func TestCannotSendNonKnockViaSendKnock(t *testing.T) {
	testValidationForSendMembershipEndpoint(t, "/_matrix/federation/v1/send_knock", "knock",
		map[string]interface{}{
			"preset":       "public_chat",
			"room_version": "7",
		},
	)
}
