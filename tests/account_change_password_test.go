package tests

import (
	"io/ioutil"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"

	"github.com/tidwall/gjson"
)

func TestChangePassword(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	oldPassword := "superuser"
	newPassword := "my_new_password"
	passwordClient := deployment.RegisterUser(t, "hs1", "test_change_password_user", oldPassword)
	unauthedClient := deployment.Client(t, "hs1", "")
	sessionTest := createSession(t, deployment, "test_change_password_user", "superuser")
	// sytest: After changing password, can't log in with old password
	t.Run("After changing password, can't log in with old password", func(t *testing.T) {

		changePassword(t, passwordClient, oldPassword, newPassword)

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
		sessionOptional := createSession(t, deployment, "test_change_password_user", newPassword)
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"auth": map[string]interface{}{
				"type":     "m.login.password",
				"user":     passwordClient.UserID,
				"password": newPassword,
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

func createSession(t *testing.T, deployment *docker.Deployment, userID, password string) *client.CSAPI {
	authedClient := deployment.Client(t, "hs1", "")
	reqBody := client.WithJSONBody(t, map[string]interface{}{
		"identifier": map[string]interface{}{
			"type": "m.id.user",
			"user": userID,
		},
		"type":     "m.login.password",
		"password": password,
	})
	res := authedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "login"}, reqBody)
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("unable to read response body: %v", err)
	}

	authedClient.UserID = gjson.GetBytes(body, "user_id").Str
	authedClient.AccessToken = gjson.GetBytes(body, "access_token").Str
	return authedClient
}
