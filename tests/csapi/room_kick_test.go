package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

// sytest: Users cannot kick users from a room they are not in
func TestCannotKickNonPresentUser(t *testing.T) {
	deployment := complement.Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	resp := alice.Do(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "kick"},
		client.WithJSONBody(t, map[string]interface{}{
			"user_id": bob.UserID,
			"reason":  "testing",
		}),
	)

	must.MatchResponse(t, resp, match.HTTPResponse{
		StatusCode: 403,
	})
}

// sytest: Users cannot kick users who have already left a room
func TestCannotKickLeftUser(t *testing.T) {
	deployment := complement.Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	bob.MustJoinRoom(t, roomID, nil)

	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	bob.MustLeaveRoom(t, roomID)

	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(bob.UserID, roomID))

	resp := alice.Do(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "kick"},
		client.WithJSONBody(t, map[string]interface{}{
			"user_id": bob.UserID,
			"reason":  "testing",
		}),
	)

	must.MatchResponse(t, resp, match.HTTPResponse{
		StatusCode: 403,
	})
}
