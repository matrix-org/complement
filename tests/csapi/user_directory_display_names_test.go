//go:build !dendrite_blacklist
// +build !dendrite_blacklist

// Rationale for being included in Dendrite's blacklist: https://github.com/matrix-org/complement/pull/199#issuecomment-904852233
package csapi_tests

import (
	"strings"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

const alicePublicName = "Alice Cooper"
const alicePrivateName = "Freddy"

var justAliceByPublicName = func(alice *client.CSAPI) []match.JSON {
	return []match.JSON{
		match.JSONKeyArrayOfSize("results", 1),
		match.JSONKeyEqual("results.0.display_name", alicePublicName),
		match.JSONKeyEqual("results.0.user_id", alice.UserID),
	}
}

var noResults = []match.JSON{
	match.JSONKeyArrayOfSize("results", 0),
}

func setupUsers(t *testing.T) (*client.CSAPI, *client.CSAPI, *client.CSAPI, func(*testing.T)) {
	// Originally written to reproduce https://github.com/matrix-org/synapse/issues/5677
	// In that bug report,
	// - Bob knows about Alice, and
	// - Alice has revealed a private name to another friend X,
	// - Bob can see that private name when he shouldn't be able to.
	//
	// I've tweaked the names to be more traditional:
	// - Eve knows about Alice,
	// - Alice reveals a private name to another friend Bob
	// - Eve shouldn't be able to see that private name via the directory.
	// Use a clean deployment for these tests so the user directory isn't polluted.
	deployment := complement.OldDeploy(t, b.BlueprintCleanHS)
	cleanup := func(t *testing.T) {
		deployment.Destroy(t)
	}

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
	})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "bob",
	})
	eve := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "eve",
	})

	// Alice sets her profile displayname. This ensures that her
	// public name, private name and userid localpart are all
	// distinguishable, even case-insensitively.
	alice.MustSetDisplayName(t, alicePublicName)

	// Alice creates a public room (so when Eve searches, she can see that Alice exists)
	alice.MustCreateRoom(t, map[string]interface{}{"visibility": "public"})
	return alice, bob, eve, cleanup
}

func checkExpectations(t *testing.T, alice, bob, eve *client.CSAPI) {
	t.Run("Eve can find Alice by profile display name", func(t *testing.T) {
		res := eve.MustDo(
			t,
			"POST",
			[]string{"_matrix", "client", "v3", "user_directory", "search"},
			client.WithJSONBody(t, map[string]interface{}{
				"search_term": alicePublicName,
			}),
		)
		must.MatchResponse(t, res, match.HTTPResponse{JSON: justAliceByPublicName(alice)})
	})

	// Previously, this test searched for the literal mxid in the search_term.
	// This was finnicky, because certain characters in the mxid would cause this test to fail, specifically '-'.
	// Unfortunately, '-' is used by Complement when generating user ID localparts.
	// See https://github.com/matrix-org/synapse/issues/13807 and specifically
	// https://github.com/matrix-org/synapse/blob/888a29f4127723a8d048ce47cff37ee8a7a6f1b9/synapse/storage/databases/main/user_directory.py#L910-L924
	// The net result is that we cannot search for a user by the complete user ID, nor can we search for the
	// localpart suffix, as the code only does prefix matching.
	// The thing we /can/ search on is the mxid up to the '-', so let's do that.
	searchTerms := strings.Split(alice.UserID, "-")
	t.Run("Eve can find Alice by mxid", func(t *testing.T) {
		res := eve.MustDo(
			t,
			"POST",
			[]string{"_matrix", "client", "v3", "user_directory", "search"},
			client.WithJSONBody(t, map[string]interface{}{
				"search_term": searchTerms[0],
			}),
		)
		must.MatchResponse(t, res, match.HTTPResponse{JSON: justAliceByPublicName(alice)})
	})

	t.Run("Eve cannot find Alice by room-specific name that Eve is not privy to", func(t *testing.T) {
		res := eve.MustDo(
			t,
			"POST",
			[]string{"_matrix", "client", "v3", "user_directory", "search"},
			client.WithJSONBody(t, map[string]interface{}{
				"search_term": alicePrivateName,
			}),
		)
		must.MatchResponse(t, res, match.HTTPResponse{JSON: noResults})
	})

	t.Run("Bob can find Alice by profile display name", func(t *testing.T) {
		res := bob.MustDo(
			t,
			"POST",
			[]string{"_matrix", "client", "v3", "user_directory", "search"},
			client.WithJSONBody(t, map[string]interface{}{
				"search_term": alicePublicName,
			}),
		)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: justAliceByPublicName(alice),
		})
	})

	t.Run("Bob can find Alice by mxid", func(t *testing.T) {
		res := bob.MustDo(
			t,
			"POST",
			[]string{"_matrix", "client", "v3", "user_directory", "search"},
			client.WithJSONBody(t, map[string]interface{}{
				"search_term": searchTerms[0],
			}),
		)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: justAliceByPublicName(alice),
		})
	})
}

func TestRoomSpecificUsernameChange(t *testing.T) {
	alice, bob, eve, cleanup := setupUsers(t)
	defer cleanup(t)

	// Bob creates a new room and invites Alice.
	privateRoom := bob.MustCreateRoom(t, map[string]interface{}{
		"visibility": "private",
		"invite":     []string{alice.UserID},
	})

	// Alice waits until she sees the invite, then accepts.
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, privateRoom))
	alice.MustJoinRoom(t, privateRoom, nil)

	// Alice reveals her private name to Bob
	alice.MustDo(
		t,
		"PUT",
		[]string{"_matrix", "client", "v3", "rooms", privateRoom, "state", "m.room.member", alice.UserID},
		client.WithJSONBody(t, map[string]interface{}{
			"displayname": alicePrivateName,
			"membership":  "join",
		}),
	)

	checkExpectations(t, alice, bob, eve)
}

func TestRoomSpecificUsernameAtJoin(t *testing.T) {
	alice, bob, eve, cleanup := setupUsers(t)
	defer cleanup(t)

	// Bob creates a new room and invites Alice.
	privateRoom := bob.MustCreateRoom(t, map[string]interface{}{
		"visibility": "private",
		"invite":     []string{alice.UserID},
	})

	// Alice waits until she sees the invite, then accepts.
	// When she accepts, she does so with a specific displayname.
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, privateRoom))
	alice.MustJoinRoom(t, privateRoom, nil)

	// Alice reveals her private name to Bob
	alice.MustDo(
		t,
		"PUT",
		[]string{"_matrix", "client", "v3", "rooms", privateRoom, "state", "m.room.member", alice.UserID},
		client.WithJSONBody(t, map[string]interface{}{
			"displayname": alicePrivateName,
			"membership":  "join",
		}),
	)

	checkExpectations(t, alice, bob, eve)
}
