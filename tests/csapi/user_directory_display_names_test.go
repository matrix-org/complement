//go:build !dendrite_blacklist
// +build !dendrite_blacklist

// Rationale for being included in Dendrite's blacklist: https://github.com/matrix-org/complement/pull/199#issuecomment-904852233
package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

const aliceUserID = "@alice:hs1"
const alicePublicName = "Alice Cooper"
const alicePrivateName = "Freddy"

var justAliceByPublicName = []match.JSON{
	match.JSONKeyArrayOfSize("results", 1),
	match.JSONKeyEqual("results.0.display_name", alicePublicName),
	match.JSONKeyEqual("results.0.user_id", aliceUserID),
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
	deployment := Deploy(t, b.BlueprintAlice)
	cleanup := func(t *testing.T) {
		deployment.Destroy(t)
	}

	alice := deployment.Client(t, "hs1", aliceUserID)
	bob := deployment.RegisterUser(t, "hs1", "bob", "bob-has-a-very-secret-pw", false)
	eve := deployment.RegisterUser(t, "hs1", "eve", "eve-has-a-very-secret-pw", false)

	// Alice sets her profile displayname. This ensures that her
	// public name, private name and userid localpart are all
	// distinguishable, even case-insensitively.
	alice.MustDoFunc(
		t,
		"PUT",
		[]string{"_matrix", "client", "v3", "profile", alice.UserID, "displayname"},
		client.WithJSONBody(t, map[string]interface{}{
			"displayname": alicePublicName,
		}),
	)

	// Alice creates a public room (so when Eve searches, she can see that Alice exists)
	alice.CreateRoom(t, map[string]interface{}{"visibility": "public"})
	return alice, bob, eve, cleanup
}

func checkExpectations(t *testing.T, bob, eve *client.CSAPI) {
	t.Run("Eve can find Alice by profile display name", func(t *testing.T) {
		res := eve.MustDoFunc(
			t,
			"POST",
			[]string{"_matrix", "client", "v3", "user_directory", "search"},
			client.WithJSONBody(t, map[string]interface{}{
				"search_term": alicePublicName,
			}),
		)
		must.MatchResponse(t, res, match.HTTPResponse{JSON: justAliceByPublicName})
	})

	t.Run("Eve can find Alice by mxid", func(t *testing.T) {
		res := eve.MustDoFunc(
			t,
			"POST",
			[]string{"_matrix", "client", "v3", "user_directory", "search"},
			client.WithJSONBody(t, map[string]interface{}{
				"search_term": aliceUserID,
			}),
		)
		must.MatchResponse(t, res, match.HTTPResponse{JSON: justAliceByPublicName})
	})

	t.Run("Eve cannot find Alice by room-specific name that Eve is not privy to", func(t *testing.T) {
		res := eve.MustDoFunc(
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
		res := bob.MustDoFunc(
			t,
			"POST",
			[]string{"_matrix", "client", "v3", "user_directory", "search"},
			client.WithJSONBody(t, map[string]interface{}{
				"search_term": alicePublicName,
			}),
		)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: justAliceByPublicName,
		})
	})

	t.Run("Bob can find Alice by mxid", func(t *testing.T) {
		res := bob.MustDoFunc(
			t,
			"POST",
			[]string{"_matrix", "client", "v3", "user_directory", "search"},
			client.WithJSONBody(t, map[string]interface{}{
				"search_term": aliceUserID,
			}),
		)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: justAliceByPublicName,
		})
	})
}

func TestRoomSpecificUsernameChange(t *testing.T) {
	t.Parallel()

	alice, bob, eve, cleanup := setupUsers(t)
	defer cleanup(t)

	// Bob creates a new room and invites Alice.
	privateRoom := bob.CreateRoom(t, map[string]interface{}{
		"visibility": "private",
		"invite":     []string{alice.UserID},
	})

	// Alice waits until she sees the invite, then accepts.
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, privateRoom))
	alice.JoinRoom(t, privateRoom, nil)

	// Alice reveals her private name to Bob
	alice.MustDoFunc(
		t,
		"PUT",
		[]string{"_matrix", "client", "v3", "rooms", privateRoom, "state", "m.room.member", alice.UserID},
		client.WithJSONBody(t, map[string]interface{}{
			"displayname": alicePrivateName,
			"membership":  "join",
		}),
	)

	checkExpectations(t, bob, eve)
}

func TestRoomSpecificUsernameAtJoin(t *testing.T) {
	t.Parallel()

	alice, bob, eve, cleanup := setupUsers(t)
	defer cleanup(t)

	// Bob creates a new room and invites Alice.
	privateRoom := bob.CreateRoom(t, map[string]interface{}{
		"visibility": "private",
		"invite":     []string{alice.UserID},
	})

	// Alice waits until she sees the invite, then accepts.
	// When she accepts, she does so with a specific displayname.
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, privateRoom))
	alice.JoinRoom(t, privateRoom, nil)

	// Alice reveals her private name to Bob
	alice.MustDoFunc(
		t,
		"PUT",
		[]string{"_matrix", "client", "v3", "rooms", privateRoom, "state", "m.room.member", alice.UserID},
		client.WithJSONBody(t, map[string]interface{}{
			"displayname": alicePrivateName,
			"membership":  "join",
		}),
	)

	checkExpectations(t, bob, eve)
}
