package tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestKeysQueryWithDeviceIDAsStringFails(t *testing.T) {
	deployment := Deploy(t, "user_query_keys", b.BlueprintAlice)
	defer deployment.Destroy(t)

	userID := "@alice:hs1"
	alice := deployment.Client(t, "hs1", userID)
	res, err := alice.DoWithAuth(t, "POST", []string{"_matrix", "client", "r0", "keys", "query"}, map[string]interface{}{
		"device_keys": map[string]interface{}{
			"@bob:hs1": map[string]bool{
				"device_id1": true,
				"device_id2": true,
			},
		},
	})
	must.NotError(t, "Failed to perform POST", err)
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 400,
		JSON: []match.JSON{
			match.JSONKeyEqual("errcode", "M_BAD_JSON"),
		},
	})
}
