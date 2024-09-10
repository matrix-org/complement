package csapi_tests

import (
	"io"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"

	"github.com/tidwall/gjson"
)

func TestChangePassword(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	password1 := "superuser"
	password2 := "my_new_password"
	passwordClient := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		Password: password1,
	})
	unauthedClient := deployment.UnauthenticatedClient(t, "hs1")
	_, sessionTest := createSession(t, deployment, passwordClient.UserID, "superuser")
	// sytest: After changing password, can't log in with old password
	t.Run("After changing password, can't log in with old password", func(t *testing.T) {

		changePassword(t, passwordClient, password1, password2)

		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"identifier": map[string]interface{}{
				"type": "m.id.user",
				"user": passwordClient.UserID,
			},
			"type":     "m.login.password",
			"password": password1,
		})
		res := unauthedClient.Do(t, "POST", []string{"_matrix", "client", "v3", "login"}, reqBody)
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
			"password": password2,
		})
		res := unauthedClient.Do(t, "POST", []string{"_matrix", "client", "v3", "login"}, reqBody)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyEqual("user_id", passwordClient.UserID),
			},
		})
	})
	// sytest: After changing password, existing session still works
	t.Run("After changing password, existing session still works", func(t *testing.T) {
		res := passwordClient.Do(t, "GET", []string{"_matrix", "client", "v3", "account", "whoami"})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
		})
	})
	// sytest: After changing password, a different session no longer works by default
	t.Run("After changing password, a different session no longer works by default", func(t *testing.T) {
		res := sessionTest.Do(t, "GET", []string{"_matrix", "client", "v3", "account", "whoami"})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 401,
		})
	})

	// sytest: After changing password, different sessions can optionally be kept
	t.Run("After changing password, different sessions can optionally be kept", func(t *testing.T) {
		_, sessionOptional := createSession(t, deployment, passwordClient.UserID, password2)
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"auth": map[string]interface{}{
				"type":     "m.login.password",
				"user":     passwordClient.UserID,
				"password": password2,
			},
			"new_password":   "new_optional_password",
			"logout_devices": false,
		})

		res := passwordClient.Do(t, "POST", []string{"_matrix", "client", "v3", "account", "password"}, reqBody)

		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
		})
		res = sessionOptional.Do(t, "GET", []string{"_matrix", "client", "v3", "account", "whoami"})

		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
		})
	})
}

func changePassword(t *testing.T, passwordClient *client.CSAPI, oldPassword string, newPassword string) {
	t.Helper()
	reqBody := client.WithJSONBody(t, map[string]interface{}{
		"auth": map[string]interface{}{
			"type": "m.login.password",
			"identifier": map[string]interface{}{
				"type": "m.id.user",
				"user": passwordClient.UserID,
			},
			"password": oldPassword,
		},
		"new_password": newPassword,
	})

	res := passwordClient.Do(t, "POST", []string{"_matrix", "client", "v3", "account", "password"}, reqBody)

	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 200,
	})
}

func createSession(t *testing.T, deployment complement.Deployment, userID, password string) (deviceID string, authedClient *client.CSAPI) {
	authedClient = deployment.UnauthenticatedClient(t, "hs1")
	reqBody := client.WithJSONBody(t, map[string]interface{}{
		"identifier": map[string]interface{}{
			"type": "m.id.user",
			"user": userID,
		},
		"type":     "m.login.password",
		"password": password,
	})
	res := authedClient.Do(t, "POST", []string{"_matrix", "client", "v3", "login"}, reqBody)
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("unable to read response body: %v", err)
	}

	authedClient.UserID = gjson.GetBytes(body, "user_id").Str
	authedClient.AccessToken = gjson.GetBytes(body, "access_token").Str
	deviceID = gjson.GetBytes(body, "device_id").Str
	return deviceID, authedClient
}
