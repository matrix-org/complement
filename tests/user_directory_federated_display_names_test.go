package tests

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestRoomSpecificUsernameHandlingOverFederation(t *testing.T) {
	// Another test case for https://github.com/matrix-org/synapse/issues/5677
	// Now we're checking that we don't leak per-room names of a remote user.

	// The scenario:
	// - Charlie (hs2) and Bob (hs1) are in a public room.
	// - Charlie and Bob are also in a private room, and Charlie has a per-room name there.
	// - Eve (hs1) shouldn't be able to see that private name.
	deployment := Deploy(t, b.BlueprintFederationTwoLocalOneRemote)
	defer deployment.Destroy(t)

	bob := deployment.Client(t, "hs1", "@bob:hs1")
	remoteCharlie := deployment.Client(t, "hs2", "@charlie:hs2")
	eve := deployment.RegisterUser(t, "hs1", "eve", "eve-has-a-very-secret-pw")

	// Charlie sets her profile displayname. This ensures that her
	// public name, private name and userid localpart are all
	// distinguishable, even case-insensitively.
	const charliePublicName = "Charlie Cooper"
	const charlieLocalPart = "charlie"
	remoteCharlie.MustDoFunc(
		t,
		"PUT",
		[]string{"_matrix", "client", "r0", "profile", remoteCharlie.UserID, "displayname"},
		client.WithJSONBody(t, map[string]interface{}{
			"displayname": charliePublicName,
		}),
	)

	// Charlie creates a public room and invites Bob (so Eve can see that Charlie exists)
	publicRoom := remoteCharlie.CreateRoom(t, map[string]interface{}{
		"visibility": "public",
		"invite":     []string{bob.UserID},
	})

	// Charlie also creates a private room with Bob.
	privateRoom := remoteCharlie.CreateRoom(t, map[string]interface{}{
		"visibility": "private",
		"invite":     []string{bob.UserID},
	})

	// Bob accepts both invites.
	bob.SyncUntilInvitedTo(t, privateRoom)
	bob.SyncUntilInvitedTo(t, publicRoom)
	bob.JoinRoom(t, publicRoom, []string{"hs2"})
	bob.JoinRoom(t, privateRoom, []string{"hs2"})

	// Charlie reveals her private name to Bob
	const charliePrivateName = "Freddy"
	remoteCharlie.MustDoFunc(
		t,
		"PUT",
		[]string{"_matrix", "client", "r0", "rooms", privateRoom, "state", "m.room.member", remoteCharlie.UserID},
		client.WithJSONBody(t, map[string]interface{}{
			"displayname": charliePrivateName,
			"membership":  "join",
		}),
	)

	// Wait for Bob to see the name change
	bob.SyncUntilTimelineHas(t, privateRoom, func(ev gjson.Result) bool {
		return ev.Get("type").Str == "m.room.member" &&
			ev.Get("state_key").Str == remoteCharlie.UserID &&
			ev.Get("content.displayname").Str == charliePrivateName
	})

	// There's no way to know what a remote user's "public profile" is
	// without making a federation request. Accept either
	// mxid or their profile's name as the displayname.
	justCharlieByPublicNameOrMxid := []match.JSON{
		match.JSONKeyArrayOfSize("results", 1),
		match.AnyOf(
			match.JSONKeyEqual("results.0.display_name", charliePublicName),
			match.JSONKeyEqual("results.0.display_name", charlieLocalPart),
			match.JSONKeyEqual("results.0.display_name", remoteCharlie.UserID),
		),
		match.JSONKeyEqual("results.0.user_id", remoteCharlie.UserID),
	}

	t.Run("Eve can find Charlie by profile display name", func(t *testing.T) {
		res := eve.MustDoFunc(
			t,
			"POST",
			[]string{"_matrix", "client", "r0", "user_directory", "search"},
			client.WithJSONBody(t, map[string]interface{}{
				"search_term": charliePublicName,
			}),
		)
		must.MatchResponse(t, res, match.HTTPResponse{JSON: justCharlieByPublicNameOrMxid})
	})

	t.Run("Eve can find Charlie by mxid", func(t *testing.T) {
		res := eve.MustDoFunc(
			t,
			"POST",
			[]string{"_matrix", "client", "r0", "user_directory", "search"},
			client.WithJSONBody(t, map[string]interface{}{
				"search_term": remoteCharlie.UserID,
			}),
		)
		must.MatchResponse(t, res, match.HTTPResponse{JSON: justCharlieByPublicNameOrMxid})
	})

	noResults := []match.JSON{
		match.JSONKeyArrayOfSize("results", 0),
	}

	t.Run("Eve cannot find Charlie by room-specific name that Eve is not privy to",
		func(t *testing.T) {
			res := eve.MustDoFunc(
				t,
				"POST",
				[]string{"_matrix", "client", "r0", "user_directory", "search"},
				client.WithJSONBody(t, map[string]interface{}{
					"search_term": charliePrivateName,
				}),
			)
			must.MatchResponse(t, res, match.HTTPResponse{JSON: noResults})
		})

	t.Run("Bob can find Charlie by profile display name", func(t *testing.T) {
		res := bob.MustDoFunc(
			t,
			"POST",
			[]string{"_matrix", "client", "r0", "user_directory", "search"},
			client.WithJSONBody(t, map[string]interface{}{
				"search_term": charliePublicName,
			}),
		)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: justCharlieByPublicNameOrMxid,
		})
	})

	t.Run("Bob can find Charlie by mxid", func(t *testing.T) {
		res := bob.MustDoFunc(
			t,
			"POST",
			[]string{"_matrix", "client", "r0", "user_directory", "search"},
			client.WithJSONBody(t, map[string]interface{}{
				"search_term": remoteCharlie.UserID,
			}),
		)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: justCharlieByPublicNameOrMxid,
		})
	})
}
