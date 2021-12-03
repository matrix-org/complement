package csapi_tests

import (
	"net/url"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
)

// The Spec says here
//     https://spec.matrix.org/v1.1/client-server-api/#server-behaviour-13
// that
// > Servers must not send room invites from ignored users to clients.
//
// Synapse does not have this property, as detailed in
// https://github.com/matrix-org/synapse/issues/11506.
// This reproduces that bug.
func TestInviteFromIgnoredUsersDoesNotAppearInSync(t *testing.T) {
	deployment := Deploy(t, b.BlueprintCleanHS)
	defer deployment.Destroy(t)
	alice := deployment.RegisterUser(t, "hs1", "alice", "sufficiently_long_password_alice")
	bob := deployment.RegisterUser(t, "hs1", "bob", "sufficiently_long_password_bob")
	chris := deployment.RegisterUser(t, "hs1", "chris", "sufficiently_long_password_chris")

	// Alice creates a room for herself.
	public_room := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	// Alice ignores Bob.
	alice.MustDoFunc(
		t,
		"PUT",
		[]string{"_matrix", "client", "v3", "user", alice.UserID, "account_data", "m.ignored_user_list"},
		client.WithJSONBody(t, map[string]interface{}{
			"content": map[string]interface{}{
				"ignored_users": map[string]interface{}{
					bob.UserID: map[string]interface{}{},
				},
			},
		}),
	)

	start := alice.SyncUntilTimelineHas(
		t, public_room, func(ev gjson.Result) bool {
			return ev.Get("type").Str == "m.room.member" &&
				ev.Get("state_key").Str == alice.UserID &&
				ev.Get("content.membership").Str == "join"
		},
	)

	// Bob invites Alice to a private room.
	bobRoom := bob.CreateRoom(t, map[string]interface{}{
		"preset": "private_chat",
		"invite": []string{alice.UserID},
	})

	// So does Chris.
	chrisRoom := chris.CreateRoom(t, map[string]interface{}{
		"preset": "private_chat",
		"invite": []string{alice.UserID},
	})

	// Alice waits until she's seen Chris's invite.
	alice.SyncUntilInvitedTo(t, chrisRoom)

	// We re-request the sync with a `since` token. We should see Chris's invite, but not Bob's.
	queryParams := url.Values{
		"since":   {start},
		"timeout": {"0"},
	}
	// Note: SyncUntil only runs its callback on array elements. I want to investigate an object.
	// So let's make the HTTP request more directly.
	response := alice.MustDoFunc(
		t,
		"GET",
		[]string{"_matrix", "client", "v3", "sync"},
		client.WithQueries(queryParams),
	)
	bobRoomPath := "rooms.invite." + client.GjsonEscape(bobRoom)
	chrisRoomPath := "rooms.invite." + client.GjsonEscape(chrisRoom)
	must.MatchResponse(t, response, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyMissing(bobRoomPath),
			match.JSONKeyPresent(chrisRoomPath),
		},
	})
}
