package tests

import (
	"testing"

	"encoding/json"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestChangePassword(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	unauthedClient := deployment.Client(t, "hs1", "")
	newPassword := "new_superuser"
	// sytest: After changing password, can't log in with old password
	t.Run("After changing password, can't log in with old password", func(t *testing.T) {
		passwordClient := deployment.RegisterUser(t, "hs1", "cant_login_with_old_password", "superuser")
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"auth": map[string]interface{}{
				"type":         "m.login.password",
				"user":         "@cant_login_with_old_password:hs1",
				"password":     "superuser",
				"access_token": passwordClient.AccessToken,
			},
			"new_password": newPassword,
		})
		res := passwordClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "account", "password"}, reqBody)

		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
		})

		res = unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "login"}, client.WithRawBody(json.RawMessage(`{
				"auth": {
					"type": "m.login.password",
					"user": "@cant_login_with_old_password:hs1",
					"password": "superuser"
				}
			}`)))
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 400,
		})
	})
}
