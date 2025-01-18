// Knocking is not yet implemented on Dendrite
//go:build !dendrite_blacklist
// +build !dendrite_blacklist

// This file contains Client-Server and Federation API tests for knocking
// https://spec.matrix.org/1.2/client-server-api/#knocking-on-rooms

package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

// A reason to include in the request body when testing knock reason parameters
const testKnockReason string = "Let me in... LET ME IN!!!"

// TestKnocking tests sending knock membership events and transitioning from knock to other membership states.
// Knocking is currently an experimental feature and not in the matrix spec.
// This function tests knocking on local and remote room.
func TestKnocking(t *testing.T) {
	// v7 is required for knocking support
	doTestKnocking(t, "7", "knock")
}

func doTestKnocking(t *testing.T, roomVersion string, joinRule string) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	// Create a client for one local user
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	// Create a client for another local user
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	// Create a client for a remote user
	charlie := deployment.Register(t, "hs2", helpers.RegistrationOpts{})

	// Create a server to observe
	inviteWaiter := helpers.NewWaiter()
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleInviteRequests(func(ev gomatrixserverlib.PDU) {
			inviteWaiter.Finish()
		}),
		federation.HandleTransactionRequests(nil, nil),
	)
	cancel := srv.Listen()
	defer cancel()
	srv.UnexpectedRequestsAreErrors = false
	david := srv.UserID("david")

	// Create a room for alice and bob to test knocking with
	roomIDOne := alice.MustCreateRoom(t, map[string]interface{}{
		"preset":       "private_chat", // Set to private in order to get an invite-only room
		"room_version": roomVersion,
	})
	alice.MustInviteRoom(t, roomIDOne, david)
	inviteWaiter.Wait(t, 5*time.Second)
	serverRoomOne := srv.MustJoinRoom(t, deployment, "hs1", roomIDOne, david)

	// Test knocking between two users on the same homeserver
	knockingBetweenTwoUsersTest(t, roomIDOne, alice, bob, serverRoomOne, false, joinRule)

	// Create a room for alice and charlie to test knocking with
	roomIDTwo := alice.MustCreateRoom(t, map[string]interface{}{
		"preset":       "private_chat", // Set to private in order to get an invite-only room
		"room_version": roomVersion,
	})
	inviteWaiter = helpers.NewWaiter()
	alice.MustInviteRoom(t, roomIDTwo, david)
	inviteWaiter.Wait(t, 5*time.Second)
	serverRoomTwo := srv.MustJoinRoom(t, deployment, "hs1", roomIDTwo, david)

	// Test knocking between two users, each on a separate homeserver
	knockingBetweenTwoUsersTest(t, roomIDTwo, alice, charlie, serverRoomTwo, true, joinRule)
}

func knockingBetweenTwoUsersTest(t *testing.T, roomID string, inRoomUser, knockingUser *client.CSAPI, serverRoom *federation.ServerRoom, testFederation bool, joinRule string) {
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
				"join_rule": joinRule,
			},
		})
	})

	t.Run("Attempting to join a room with join rule 'knock' without an invite should fail", func(t *testing.T) {
		// Set server_name so we can find rooms via ID over federation
		res := knockingUser.JoinRoom(t, roomID, []string{"hs1"})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 403,
		})
	})

	t.Run("Knocking on a room with join rule 'knock' should succeed", func(t *testing.T) {
		mustKnockOnRoomSynced(t, knockingUser, roomID, testKnockReason, []string{"hs1"})
	})

	t.Run("A user that has already knocked is allowed to knock again on the same room", func(t *testing.T) {
		mustKnockOnRoomSynced(t, knockingUser, roomID, "I really like knock knock jokes", []string{"hs1"})
	})

	t.Run("Users in the room see a user's membership update when they knock", func(t *testing.T) {
		// wait for the membership to arrive over federation
		start := time.Now()
		knockerState := serverRoom.CurrentState("m.room.member", knockingUser.UserID)
		for knockerState == nil && time.Since(start) < 5*time.Second {
			time.Sleep(100 * time.Millisecond)
			knockerState = serverRoom.CurrentState("m.room.member", knockingUser.UserID)
		}

		// check the membership seen over the federation
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

		inRoomUser.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
			if ev.Get("type").Str != "m.room.member" || ev.Get("sender").Str != knockingUser.UserID {
				return false
			}
			must.Equal(t, ev.Get("content").Get("reason").Str, testKnockReason, "incorrect reason for knock")
			must.Equal(t, ev.Get("content").Get("membership").Str, "knock", "incorrect membership for knocking user")
			return true
		}))
	})

	if !testFederation {
		// Rescinding a knock over federation is currently not specced
		t.Run("A user that has knocked on a local room can rescind their knock and then knock again", func(t *testing.T) {
			// We need to carry out an incremental sync after knocking in order to get leave information
			// Carry out an initial sync here and save the since token
			_, since := knockingUser.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

			// Rescind knock
			knockingUser.MustLeaveRoom(t, roomID)

			// Use our sync token from earlier to carry out an incremental sync. Initial syncs may not contain room
			// leave information for obvious reasons
			knockingUser.MustSyncUntil(t, client.SyncReq{Since: since}, func(clientUserID string, topLevelSyncJSON gjson.Result) error {
				events := topLevelSyncJSON.Get("rooms.leave." + client.GjsonEscape(roomID) + ".timeline.events")
				if !events.Exists() {
					return fmt.Errorf("no leave section for room %s", roomID)
				}
				for _, ev := range events.Array() {
					if ev.Get("type").Str != "m.room.member" || ev.Get("sender").Str != knockingUser.UserID {
						continue
					}
					must.Equal(t, ev.Get("content").Get("membership").Str, "leave", "expected leave membership after rescinding a knock")
					return nil
				}
				return fmt.Errorf("leave timeline for %s doesn't have leave event for %s", roomID, knockingUser.UserID)
			})

			// Knock again to return us to the knocked state
			mustKnockOnRoomSynced(t, knockingUser, roomID, "Let me in... again?", []string{"hs1"})
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
			[]string{"_matrix", "client", "v3", "rooms", roomID, "kick"},
			client.WithJSONBody(t, map[string]string{
				"user_id": knockingUser.UserID,
				"reason":  "I don't think so",
			}),
		)

		// Wait until the leave membership event has come down sync
		inRoomUser.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
			return ev.Get("type").Str != "m.room.member" ||
				ev.Get("state_key").Str != knockingUser.UserID ||
				ev.Get("content").Get("membership").Str != "leave"
		}))

		// Knock again
		mustKnockOnRoomSynced(t, knockingUser, roomID, "Pleeease let me in?", []string{"hs1"})
	})

	t.Run("A user can knock on a room without a reason", func(t *testing.T) {
		// Reject the knock
		inRoomUser.MustDo(
			t,
			"POST",
			[]string{"_matrix", "client", "v3", "rooms", roomID, "kick"},
			client.WithJSONBody(t, map[string]string{
				"user_id": knockingUser.UserID,
				"reason":  "Please try again",
			}),
		)

		// Knock again, this time without a reason
		mustKnockOnRoomSynced(t, knockingUser, roomID, "", []string{"hs1"})
	})

	t.Run("A user in the room can accept a knock", func(t *testing.T) {
		inRoomUser.MustInviteRoom(t, roomID, knockingUser.UserID)

		// Wait until the invite membership event has come down sync
		inRoomUser.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
			return ev.Get("type").Str != "m.room.member" ||
				ev.Get("state_key").Str != knockingUser.UserID ||
				ev.Get("content").Get("membership").Str != "invite"
		}))
	})

	t.Run("A user cannot knock on a room they are already invited to", func(t *testing.T) {
		reason := "I'm sticking my hand out the window and knocking again!"
		knockOnRoomWithStatus(t, knockingUser, roomID, reason, []string{"hs1"}, 403)
	})

	t.Run("A user cannot knock on a room they are already in", func(t *testing.T) {
		knockingUser.MustJoinRoom(t, roomID, []string{"hs1"})
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
			[]string{"_matrix", "client", "v3", "rooms", roomID, "ban"},
			client.WithJSONBody(t, map[string]string{
				"user_id": knockingUser.UserID,
				"reason":  "Turns out Bob wasn't that trustworthy after all!",
			}),
		)

		// Wait until the ban membership event has come down sync
		inRoomUser.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
			return ev.Get("type").Str != "m.room.member" ||
				ev.Get("state_key").Str != knockingUser.UserID ||
				ev.Get("content").Get("membership").Str != "ban"
		}))

		knockOnRoomWithStatus(t, knockingUser, roomID, "I didn't mean it!", []string{"hs1"}, 403)
	})
}

func syncKnockedOn(userID, roomID string) client.SyncCheckOpt {
	return func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		// two forms which depend on what the client user is:
		// - passively viewing a membership for a room you're joined in
		// - actively leaving the room
		if clientUserID == userID {
			events := topLevelSyncJSON.Get("rooms.knock." + client.GjsonEscape(roomID) + ".knock_state.events")
			if events.Exists() && events.IsArray() {
				// We don't currently define any required state event types to be sent.
				// If we've reached this point, then an entry for this room was found
				return nil
			}
			return fmt.Errorf("no knock section for room %s", roomID)
		}

		// passive
		return client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
			return ev.Get("type").Str == "m.room.member" && ev.Get("state_key").Str == userID && ev.Get("content.membership").Str == "knock"
		})(clientUserID, topLevelSyncJSON)
	}
}

// mustKnockOnRoomSynced will knock on a given room on the behalf of a user, and block until the knock has persisted.
// serverNames should be populated if knocking on a room that the user's homeserver isn't currently a part of.
// Fails the test if the knock response does not return a 200 status code.
func mustKnockOnRoomSynced(t *testing.T, c *client.CSAPI, roomID, reason string, serverNames []string) {
	knockOnRoomWithStatus(t, c, roomID, reason, serverNames, 200)

	// The knock should have succeeded. Block until we see the knock appear down sync
	c.MustSyncUntil(t, client.SyncReq{}, syncKnockedOn(c.UserID, roomID))
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
	res := c.Do(
		t,
		"POST",
		[]string{"_matrix", "client", "v3", "knock", roomID},
		client.WithQueries(query),
		client.WithRawBody(b),
	)
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: expectedStatus,
	})
}

// TestKnockRoomsInPublicRoomsDirectory will create a knock room, attempt to publish it to the public rooms directory,
// and then check that the room appears in the directory. The room's entry should also have a 'join_rule' field
// representing a knock room. For sanity-checking, this test will also create a public room and ensure it has a
// 'join_rule' representing a publicly-joinable room.
func TestKnockRoomsInPublicRoomsDirectory(t *testing.T) {
	// v7 is required for knocking
	doTestKnockRoomsInPublicRoomsDirectory(t, "7", "knock")
}

func doTestKnockRoomsInPublicRoomsDirectory(t *testing.T, roomVersion string, joinRule string) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	// Create a client for a local user
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	// Create an invite-only room with the knock room version
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset":       "private_chat", // Set to private in order to get an invite-only room
		"room_version": roomVersion,
	})

	// Change the join_rule to allow knocking
	emptyStateKey := ""
	alice.SendEventSynced(t, roomID, b.Event{
		Type:     "m.room.join_rules",
		Sender:   alice.UserID,
		StateKey: &emptyStateKey,
		Content: map[string]interface{}{
			"join_rule": joinRule,
		},
	})

	// Publish the room to the public room directory and check that the 'join_rule' key is knock
	publishAndCheckRoomJoinRule(t, alice, roomID, joinRule)

	// Create a public room
	roomID = alice.MustCreateRoom(t, map[string]interface{}{
		"preset":       "public_chat",
		"room_version": roomVersion,
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
		[]string{"_matrix", "client", "v3", "directory", "list", "room", roomID},
		client.WithJSONBody(t, map[string]string{
			"visibility": "public",
		}),
	)

	// Check that we can see the room in the directory
	c.MustDo(t, "GET", []string{"_matrix", "client", "v3", "publicRooms"},
		client.WithRetryUntil(time.Second, func(res *http.Response) bool {
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
				t.Logf("Room was not present in public room directory response")
			}
			return roomFound
		}),
	)
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
