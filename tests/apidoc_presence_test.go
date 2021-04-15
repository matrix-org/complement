package tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestPresence(t *testing.T) {
	deployment := Deploy(t, "login", b.BlueprintAlice)
	defer deployment.Destroy(t)
	unauthedClient := deployment.Client(t, "hs1", "")
	// sytest: GET /presence/:user_id/status fetches initial status
	t.Run("GET /presence/:user_id/status fetches initial status", func(t *testing.T) {
		t.Parallel()
		CreateDummyUser(t, unauthedClient, "user_presence")

		res := unauthedClient.MustDo(t, "GET", []string{"_matrix", "client", "r0", "presence", "user_presence", "status"}, nil)

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyPresent("presence"),
			},
		})
	})
}
