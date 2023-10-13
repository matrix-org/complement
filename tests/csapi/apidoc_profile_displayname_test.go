package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

func TestProfileDisplayName(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	unauthedClient := deployment.UnauthenticatedClient(t, "hs1")
	authedClient := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	displayName := "my_display_name"
	// sytest: PUT /profile/:user_id/displayname sets my name
	t.Run("PUT /profile/:user_id/displayname sets my name", func(t *testing.T) {
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"displayname": displayName,
		})
		_ = authedClient.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "profile", authedClient.UserID, "displayname"}, reqBody)
	})
	// sytest: GET /profile/:user_id/displayname publicly accessible
	t.Run("GET /profile/:user_id/displayname publicly accessible", func(t *testing.T) {
		res := unauthedClient.Do(t, "GET", []string{"_matrix", "client", "v3", "profile", authedClient.UserID, "displayname"})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyEqual("displayname", displayName),
			},
		})
	})
}
