package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestRoomSpecificUsernameNotLeaked(t *testing.T) {
	// Reproduces https://github.com/matrix-org/synapse/issues/5677
	// In that bug report,
	// - Bob knows about Alice, and
	// - Alice has revealed a private name to another friend X,
	// - Bob can see that private name when he shouldn't be able to.
	//
	// I've tweaked the names to be more traditional:
	// - Even knows about Alice,
	// - Alice reveals a private name to another friend Bob
	// - Eve shouldn't be able to see that private name.
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.RegisterUser(t, "hs1", "bob", "bob-has-a-very-secret-pw")
	eve := deployment.RegisterUser(t, "hs1", "eve", "eve-has-a-very-secret-pw")

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
	alice.MustDoFunc(
		t,
		"PUT",
		[]string{"_matrix", "client", "r0", "rooms", privateRoom, "state", "m.room.member", "@alice:hs1"},
		client.WithJSONBody(t, map[string]interface{}{
			"displayname": "Alice Cooper",
			"membership":  "join",
		}),
	)

	// Eve should be able to find alice by her public name and mxid
	for _, name := range []string{"alice", alice.UserID} {
		res := eve.SearchUserDirectory(t, name)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("results", 1),
				match.JSONKeyEqual("results.0.display_name", "alice"),
				match.JSONKeyEqual("results.0.user_id", alice.UserID),
			},
		})
	}

	// Eve should not be able to find Alice by her private name
	res := eve.SearchUserDirectory(t, "Alice Cooper")
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyArrayOfSize("results", 0),
		},
	})
}
