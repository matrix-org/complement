package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

// sytest: Users cannot kick users from a room they are not in
func TestCannotKickNonPresentUser(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	resp := alice.DoFunc(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "kick"},
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
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	bob.JoinRoom(t, roomID, nil)

	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	bob.LeaveRoom(t, roomID)

	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(bob.UserID, roomID))

	resp := alice.DoFunc(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "kick"},
		client.WithJSONBody(t, map[string]interface{}{
			"user_id": bob.UserID,
			"reason":  "testing",
		}),
	)

	must.MatchResponse(t, resp, match.HTTPResponse{
		StatusCode: 403,
	})
}
