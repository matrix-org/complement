package csapi_tests

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
	password1 := "superuser"
	password2 := "my_new_password"
	passwordClient := deployment.RegisterUser(t, "hs1", "test_change_password_user", password1, false)
	unauthedClient := deployment.Client(t, "hs1", "")
	sessionTest := deployment.Client(t, "hs1", "")
	sessionTest.UserID, sessionTest.AccessToken = sessionTest.MustLogin(t, "test_change_password_user", "superuser")
	// sytest: After changing password, can't log in with old password
	t.Run("After changing password, can't log in with old password", func(t *testing.T) {

		changePassword(t, passwordClient, password1, password2)
		res := passwordClient.Login(t, passwordClient.UserID, password1)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 403,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_FORBIDDEN"),
			},
		})
	})
	// sytest: After changing password, can log in with new password
	t.Run("After changing password, can log in with new password", func(t *testing.T) {
		res := unauthedClient.Login(t, passwordClient.UserID, password2)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyEqual("user_id", passwordClient.UserID),
			},
		})
	})
	// sytest: After changing password, existing session still works
	t.Run("After changing password, existing session still works", func(t *testing.T) {
		res := passwordClient.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "account", "whoami"})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
		})
	})
	// sytest: After changing password, a different session no longer works by default
	t.Run("After changing password, a different session no longer works by default", func(t *testing.T) {
		res := sessionTest.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "account", "whoami"})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 401,
		})
	})

	// sytest: After changing password, different sessions can optionally be kept
	t.Run("After changing password, different sessions can optionally be kept", func(t *testing.T) {
		sessionOptional := deployment.Client(t, "hs1", "")
		sessionOptional.UserID, sessionOptional.AccessToken = sessionOptional.MustLogin(t, "test_change_password_user", password2)
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"auth": map[string]interface{}{
				"type":     "m.login.password",
				"user":     passwordClient.UserID,
				"password": password2,
			},
			"new_password":   "new_optional_password",
			"logout_devices": false,
		})

		res := passwordClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "account", "password"}, reqBody)

		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
		})
		res = sessionOptional.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "account", "whoami"})

		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
		})
	})
}

func changePassword(t *testing.T, passwordClient *client.CSAPI, oldPassword string, newPassword string) {
	t.Helper()
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
