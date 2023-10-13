package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

func TestProfileAvatarURL(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	unauthedClient := deployment.UnauthenticatedClient(t, "hs1")
	authedClient := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	avatarURL := "mxc://example.com/SEsfnsuifSDFSSEF"
	// sytest: PUT /profile/:user_id/avatar_url sets my avatar
	t.Run("PUT /profile/:user_id/avatar_url sets my avatar", func(t *testing.T) {
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"avatar_url": avatarURL,
		})
		res := authedClient.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "profile", authedClient.UserID, "avatar_url"}, reqBody)

		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
		})
	})

	// sytest: GET /profile/:user_id/avatar_url publicly accessible
	t.Run("GET /profile/:user_id/avatar_url publicly accessible", func(t *testing.T) {
		res := unauthedClient.Do(t, "GET", []string{"_matrix", "client", "v3", "profile", authedClient.UserID, "avatar_url"})

		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyPresent("avatar_url"),
				match.JSONKeyEqual("avatar_url", avatarURL),
			},
		})
	})
}
