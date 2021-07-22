package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestProfileAvatarURL(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	unauthedClient := deployment.Client(t, "hs1", "")
	authedClient := deployment.Client(t, "hs1", "@alice:hs1")
	avatarURL := "mxc://example.com/SEsfnsuifSDFSSEF"
	// sytest: PUT /profile/:user_id/avatar_url sets my avatar
	t.Run("PUT /profile/:user_id/avatar_url sets my avatar", func(t *testing.T) {
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"avatar_url": avatarURL,
		})
		res := authedClient.MustDoFunc(t, "PUT", []string{"_matrix", "client", "r0", "profile", authedClient.UserID, "avatar_url"}, reqBody)

		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
		})
	})

	// sytest: GET /profile/:user_id/avatar_url publicly accessible
	t.Run("GET /profile/:user_id/avatar_url publicly accessible", func(t *testing.T) {
		res := unauthedClient.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "profile", authedClient.UserID, "avatar_url"})

		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyPresent("avatar_url"),
				match.JSONKeyEqual("avatar_url", avatarURL),
			},
		})
	})
}
