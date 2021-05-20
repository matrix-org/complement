// +build !dendrite_blacklist

// Rationale for being included in Dendrite's blacklist: https://github.com/matrix-org/complement/pull/104#discussion_r617646624

package tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestPresence(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	authedClient := deployment.Client(t, "hs1", "@alice:hs1")
	// sytest: GET /presence/:user_id/status fetches initial status
	t.Run("GET /presence/:user_id/status fetches initial status", func(t *testing.T) {
		res := authedClient.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "presence", "@alice:hs1", "status"})
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyPresent("presence"),
			},
		})
	})
}
