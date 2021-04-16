package tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestPresence(t *testing.T) {
	deployment := Deploy(t, "presence", b.BlueprintAlice)
	defer deployment.Destroy(t)
	//unauthedClient to create an user which in turn creates an authedClient
	unauthedClient := deployment.Client(t, "hs1", "")
	CreateDummyUser(t, unauthedClient, "user_presence")
	authedClient := deployment.Client(t, "hs1", "user_presence")
	// sytest: GET /presence/:user_id/status fetches initial status
	t.Run("GET /presence/:user_id/status fetches initial status", func(t *testing.T) {
		res := authedClient.MustDo(t, "GET", []string{"_matrix", "client", "r0", "presence", "user_presence", "status"}, nil)

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyPresent("presence"),
			},
		})
	})
}
