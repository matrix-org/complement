package tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestSyncFilter(t *testing.T) {
	deployment := Deploy(t, "sync_filter", b.BlueprintAlice)
	defer deployment.Destroy(t)
	authedClient := deployment.Client(t, "hs1", "@alice:hs1")
	// sytest: Can create filter
	t.Run("GET /presence/:user_id/status fetches initial status", func(t *testing.T) {
		res := authedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "user", "@alice:hs1", "filter"}, `{
				"room": {
					"timeline": {
						"limit": "10"
					}
				}
			}`)

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyPresent("filter_id"),
			},
		})
	})
}
