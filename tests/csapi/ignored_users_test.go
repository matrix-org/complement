//go:build !dendrite_blacklist
// +build !dendrite_blacklist

// Rationale for being included in Dendrite's blacklist: https://github.com/matrix-org/dendrite/issues/600
package csapi_tests

import (
	"net/url"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

// The Spec says here
//
//	https://spec.matrix.org/v1.1/client-server-api/#server-behaviour-13
//
// that
// > Servers must not send room invites from ignored users to clients.
//
// This is a regression test for
// https://github.com/matrix-org/synapse/issues/11506
// to ensure that Synapse complies with this part of the spec.
func TestInviteFromIgnoredUsersDoesNotAppearInSync(t *testing.T) {
	deployment := complement.Deploy(t, b.BlueprintCleanHS)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "alice"})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "bob"})
	chris := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "chris"})

	// Alice creates a room for herself.
	publicRoom := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	// Alice waits to see the join event.
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, publicRoom))

	// Alice ignores Bob.
	alice.MustSetGlobalAccountData(t, "m.ignored_user_list", map[string]interface{}{
		"ignored_users": map[string]interface{}{
			bob.UserID: map[string]interface{}{},
		},
	})

	// Alice waits to see that the ignore was successful.
	sinceJoinedAndIgnored := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncGlobalAccountDataHas(
		func(ev gjson.Result) bool {
			t.Logf(ev.Raw + "\n")
			return ev.Get("type").Str == "m.ignored_user_list" &&
				ev.Get("content.ignored_users."+client.GjsonEscape(bob.UserID)).Exists()
		},
	))

	// Bob invites Alice to a private room.
	bobRoom := bob.MustCreateRoom(t, map[string]interface{}{
		"preset": "private_chat",
		"invite": []string{alice.UserID},
	})

	// So does Chris.
	chrisRoom := chris.MustCreateRoom(t, map[string]interface{}{
		"preset": "private_chat",
		"invite": []string{alice.UserID},
	})

	// Alice waits until she's seen Chris's invite.
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, chrisRoom))

	// We re-request the sync with a `since` token. We should see Chris's invite, but not Bob's.
	queryParams := url.Values{
		"since":   {sinceJoinedAndIgnored},
		"timeout": {"0"},
	}
	// Note: SyncUntil only runs its callback on array elements. I want to investigate an object.
	// So let's make the HTTP request more directly.
	response := alice.MustDo(
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
