package tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestProfileDisplayName(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	unauthedClient := deployment.Client(t, "hs1", "")
	authedClient := deployment.Client(t, "hs1", "@alice:hs1")
	displayName := "my_display_name"
	// sytest: PUT /profile/:user_id/displayname sets my name
	t.Run("PUT /profile/:user_id/displayname sets my name", func(t *testing.T) {
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"displayname":     displayName,
		})
		_ = authedClient.MustDoFunc(t, "PUT", []string{"_matrix", "client", "r0", "profile", authedClient.UserID, "displayname"}, reqBody)
		res := unauthedClient.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "profile", authedClient.UserID, "displayname"})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyEqual("displayname", displayName),
			},
		})
	})
	// sytest: GET /profile/:user_id/displayname publicly accessible
	t.Run("GET /profile/:user_id/displayname publicly accessible", func(t *testing.T) {
		res := unauthedClient.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "profile", authedClient.UserID, "displayname"})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyEqual("displayname", displayName),
			},
		})
	})
}
