package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestRoomSpecificUsernameHandling(t *testing.T) {
	// Originally written to reproduce https://github.com/matrix-org/synapse/issues/5677
	// In that bug report,
	// - Bob knows about Alice, and
	// - Alice has revealed a private name to another friend X,
	// - Bob can see that private name when he shouldn't be able to.
	//
	// I've tweaked the names to be more traditional:
	// - Eve knows about Alice,
	// - Alice reveals a private name to another friend Bob
	// - Eve shouldn't be able to see that private name.
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.RegisterUser(t, "hs1", "bob", "bob-has-a-very-secret-pw")
	eve := deployment.RegisterUser(t, "hs1", "eve", "eve-has-a-very-secret-pw")

	// Alice sets her profile displayname. This ensures that her
	// public name, private name and userid localpart are all
	// distinguishable, even case-insensitively.
	const alicePublicName = "Alice Cooper"
	alice.MustDoFunc(
		t,
		"PUT",
		[]string{"profile", alice.UserID, "displayname"},
		client.WithJSONBody(t, map[string]interface{}{
			"displayname": alicePublicName,
		}),
	)

	// Alice creates a public room (so Eve can see that Alice exists)
	alice.CreateRoom(t, map[string]interface{}{"visibility": "public"})

	// Bob creates a new room and invites Alice.
	privateRoom := bob.CreateRoom(t, map[string]interface{}{
		"visibility": "private",
		"invite":     []string{alice.UserID},
	})

	// Alice waits until she sees the invite, then accepts.
	alice.SyncUntilInvitedTo(t, privateRoom)
	alice.JoinRoom(t, privateRoom, nil)

	// Alice reveals her private name to Bob
	const alicePrivateName = "Freddy"
	alice.MustDoFunc(
		t,
		"PUT",
		[]string{"_matrix", "client", "r0", "rooms", privateRoom, "state", "m.room.member", "@alice:hs1"},
		client.WithJSONBody(t, map[string]interface{}{
			"displayname": alicePrivateName,
			"membership":  "join",
		}),
	)

	justAliceByPublicName := []match.JSON{
		match.JSONKeyArrayOfSize("results", 1),
		match.JSONKeyEqual("results.0.display_name", alicePublicName),
		match.JSONKeyEqual("results.0.user_id", alice.UserID),
	}

	t.Run("Eve can find Alice by profile display name",
		func(t *testing.T) {
			res := eve.SearchUserDirectory(t, alicePublicName)
			must.MatchResponse(t, res, match.HTTPResponse{JSON: justAliceByPublicName})
		})

	t.Run("Eve can find Alice by mxid",
		func(t *testing.T) {
			res := eve.SearchUserDirectory(t, alice.UserID)
			must.MatchResponse(t, res, match.HTTPResponse{JSON: justAliceByPublicName})
		})

	noResults := []match.JSON{
		match.JSONKeyArrayOfSize("results", 0),
	}

	t.Run("Eve cannot find Alice by room-specific name that Eve is not privy to",
		func(t *testing.T) {
			res := eve.SearchUserDirectory(t, alicePrivateName)
			must.MatchResponse(t, res, match.HTTPResponse{JSON: noResults})
		})

	t.Run("Bob can find Alice by profile display name",
		func(t *testing.T) {
			res := bob.SearchUserDirectory(t, alicePublicName)
			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: justAliceByPublicName,
			})
		})

	t.Run("Bob can find Alice by mxid",
		func(t *testing.T) {
			res := bob.SearchUserDirectory(t, alice.UserID)
			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: justAliceByPublicName,
			})
		})
}
