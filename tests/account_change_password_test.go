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
	oldPassword := "superuser"
	newPassword := "my_new_password"
	passwordClient := deployment.RegisterUser(t, "hs1", "test_change_password_user", oldPassword)
	unauthedClient := deployment.Client(t, "hs1", "")
	// sytest: After changing password, can't log in with old password
	t.Run("After changing password, can't log in with old password", func(t *testing.T) {

		changePassword(passwordClient, oldPassword, newPassword, t)

		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"identifier": map[string]interface{}{
				"type": "m.id.user",
				"user": passwordClient.UserID,
			},
			"type":     "m.login.password",
			"password": oldPassword,
		})
		res := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "login"}, reqBody)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 403,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_FORBIDDEN"),
			},
		})
	})
	// sytest: After changing password, can log in with new password
	t.Run("After changing password, can log in with new password", func(t *testing.T) {

		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"identifier": map[string]interface{}{
				"type": "m.id.user",
				"user": passwordClient.UserID,
			},
			"type":     "m.login.password",
			"password": newPassword,
		})
		res := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "login"}, reqBody)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
		})
	})
}

func changePassword(passwordClient *client.CSAPI, oldPassword string, newPassword string, t *testing.T) {
	reqBody := client.WithJSONBody(t, map[string]interface{}{
		"auth": map[string]interface{}{
			"type":     "m.login.password",
			"user":     passwordClient.UserID,
			"password": oldPassword,
		},
		"new_password": newPassword,
	})

	res := passwordClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "account", "password"}, reqBody)

	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 200,
	})
}
