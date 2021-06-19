package tests

import (
	"encoding/json"
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
	authedClient := deployment.RegisterUser(t, "hs1", "display_name_user", "superuser")
	displayName := "my_display_name"
	// sytest: PUT /profile/:user_id/displayname sets my name
	t.Run("PUT /profile/:user_id/displayname sets my name", func(t *testing.T) {
		setDisplayName(t, authedClient, displayName)
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
		res := unauthedClient.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "profile", "@display_name_user:hs1", "displayname"})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyEqual("displayname", displayName),
			},
		})
	})
}

func setDisplayName(t *testing.T, authedClient *client.CSAPI, displayName string) {
	_ = authedClient.MustDo(t, "PUT", []string{"_matrix", "client", "r0", "profile", authedClient.UserID, "displayname"}, json.RawMessage(`{
				"displayname": "`+displayName+`"
			}`))
}
