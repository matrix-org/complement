package tests

import (
	"encoding/json"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestSyncFilter(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	authedClient := deployment.Client(t, "hs1", "@alice:hs1")
	// sytest: Can create filter
	reqBody, err := json.Marshal(map[string]interface{}{
		"room": map[string]interface{}{
			"timeline": map[string]int{
				"limit": 10,
			},
		},
	})
	if err != nil {
		t.Fatalf("failed to marshal JSON request body: %s", err)
	}
	t.Run("Can create filter", func(t *testing.T) {
		res := authedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "user", "@alice:hs1", "filter"}, json.RawMessage(reqBody))

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyPresent("filter_id"),
			},
		})
	})
}
