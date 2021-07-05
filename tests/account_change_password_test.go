package tests

import (
	"fmt"
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
	password1 := "superuser"
	password2 := "my_new_password"
	password3 := "new_optional_password"
	passwordClient := deployment.RegisterUser(t, "hs1", "test_change_password_user", password1)
	unauthedClient := deployment.Client(t, "hs1", "")
	sessionTest := createSession(t, deployment, "test_change_password_user", "superuser")
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
			"password": password2,
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
		sessionOptional := createSession(t, deployment, "test_change_password_user", password2)
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

	// sytest: Pushers created with a different access token are deleted on password change
	t.Run("Pushers created with a different access token are deleted on password change", func(t *testing.T) {
		sessionOptional := createSession(t, deployment, "test_change_password_user", "new_optional_password")
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"data": map[string]interface{}{
				"url": "https://dummy.url/_matrix/push/v1/notify",
			},
			"profile_tag":         "tag",
			"kind":                "http",
			"app_id":              "complement",
			"app_display_name":    "complement_display_name",
			"device_display_name": "device_display_name",
			"pushkey":             "a_push_key",
			"lang":                "en",
		})

		res := sessionOptional.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "pushers", "set"}, reqBody)

		changePassword(t, passwordClient, password3, password2)

		res = passwordClient.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "pushers"})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyPresent("pushers"),
				match.JSONArrayEach("pushers", func(val gjson.Result) error {
					if len(val.Array()) != 0 {
						return fmt.Errorf("expected array length to be zero: %v", val.Raw)
					}
					return nil
				}),
			},
		})
	})

	// sytest: Pushers created with a the same access token are not deleted on password change
	t.Run("Pushers created with a the same access token are not deleted on password change", func(t *testing.T) {
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"data": map[string]interface{}{
				"url": "https://dummy.url/_matrix/push/v1/notify",
			},
			"profile_tag":         "tag",
			"kind":                "http",
			"app_id":              "complement",
			"app_display_name":    "complement_display_name",
			"device_display_name": "device_display_name",
			"pushkey":             "a_push_key",
			"lang":                "en",
		})

		res := passwordClient.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "pushers", "set"}, reqBody)

		changePassword(t, passwordClient, password2, password3)

		res = passwordClient.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "pushers"})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyPresent("pushers"),
				match.JSONArrayEach("pushers", func(val gjson.Result) error {
					if len(val.Array()) != 1 {
						return fmt.Errorf("expected array length to be one: %v", val.Raw)
					}
					return nil
				}),
			},
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
