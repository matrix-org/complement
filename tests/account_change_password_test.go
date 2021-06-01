package tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestChangePassword(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	passwordClient := deployment.RegisterUser(t, "hs1", "cant_login_with_old_password", "superuser")
	unauthedClient := deployment.Client(t, "hs1", "")
	newPassword := "new_superuser"
	// sytest: After changing password, can't log in with old password
	t.Run("After changing password, can't log in with old password", func(t *testing.T) {
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"auth": map[string]interface{}{
				"type":     "m.login.password",
				"user":     passwordClient.UserID,
				"password": "superuser",
			},
			"new_password": newPassword,
		})
		res := passwordClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "account", "password"}, reqBody)

		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
		})

		reqBody = client.WithJSONBody(t, map[string]interface{}{
			"identifier": map[string]interface{}{
				"type": "m.id.user",
				"user": passwordClient.UserID,
			},
			"type":     "m.login.password",
			"password": "superuser",
		})
		res = unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "login"}, reqBody)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 403,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_FORBIDDEN"),
			},
		})
	})
}
