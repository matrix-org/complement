// +build !synapse_blacklist

// Rationale for being included in Synapse's blacklist: https://github.com/matrix-org/synapse/issues/5677
package csapi_tests

import (
	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"testing"
)

func TestRoomSpecificUsernameNotLeaked(t *testing.T) {
	// Reproduces https://github.com/matrix-org/synapse/issues/5677
	// In that bug report, Alice has revealed a private name to a friend X,
	// and Bob can see that private name when he shouldn't be able to.
	// I've tweaked the names to be more traditional: Alice reveals a private name
	// to Bob, and Eve shouldn't be able to see that name.
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.RegisterUser(t, "hs1", "bob", "bob-pw")
	eve := deployment.RegisterUser(t, "hs1", "eve", "eve-pw")

	t.Run("Usernames specific to a room aren't leaked in the user directory", func(t *testing.T) {
		// Bob creates a new room and invites Alice. She accepts.
		privateRoom := bob.CreateRoom(t, map[string]interface{}{
			"m.federate": false,
		})
		bob.InviteRoom(t, privateRoom, "@alice:hs1")
		alice.JoinRoom(t, privateRoom, nil)

		// Alice reveals her private name to Bob
		alice.MustDo(
			t,
			"PUT",
			[]string{"_matrix", "client", "r0", "rooms", privateRoom, "state", "m.room.member",
				"@alice:hs1"},
			map[string]interface{}{
				"displayname": "Alice Cooper",
				"membership":  "join",
			},
		)

		// Eve looks up alice in the directory using her public name
		res := eve.MustDo(
			t,
			"POST",
			[]string{"_matrix", "client", "r0", "user_directory", "search"},
			map[string]interface{}{
				"search_term": "alice",
			},
		)

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("results.0.display_name", "alice"),
				match.JSONKeyEqual("results.0.user_id", "@alice:hs1"),
			},
		})
	})
}
