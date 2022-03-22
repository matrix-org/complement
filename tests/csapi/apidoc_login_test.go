package csapi_tests

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestLogin(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	unauthedClient := deployment.Client(t, "hs1", "")
	_ = deployment.RegisterUser(t, "hs1", "test_login_user", "superuser", false)
	t.Run("parallel", func(t *testing.T) {
		// sytest: GET /login yields a set of flows
		t.Run("GET /login yields a set of flows", func(t *testing.T) {
			t.Parallel()
			res := unauthedClient.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "login"}, client.WithRawBody(json.RawMessage(`{}`)))
			must.MatchResponse(t, res, match.HTTPResponse{
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
				JSON: []match.JSON{
					// TODO(paul): Spec is a little vague here. Spec says that every
					//   option needs a 'stages' key, but the implementation omits it
					//   for options that have only one stage in their flow.
					match.JSONArrayEach("flows", func(val gjson.Result) error {
						if !(val.Get("stages").IsArray()) && (val.Get("type").Raw == "") {
							return fmt.Errorf("expected flow to have a list of 'stages' or a 'type' : %v %v", val.Get("type").Raw, val.Get("stages"))
						}
						return nil
					}),
				},
			})
		})
		// sytest: POST /login can log in as a user
		t.Run("POST /login can login as user", func(t *testing.T) {
			t.Parallel()
			res := unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "login"}, json.RawMessage(`{
				"type": "m.login.password",
				"identifier": {
					"type": "m.id.user",
					"user": "@test_login_user:hs1"
				},
				"password": "superuser"
			}`))

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("access_token", gjson.String),
					match.JSONKeyEqual("home_server", "hs1"),
				},
			})
		})
		// sytest: POST /login returns the same device_id as that in the request
		t.Run("POST /login returns the same device_id as that in the request", func(t *testing.T) {
			t.Parallel()
			deviceID := "test_device_id"
			res := unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "login"}, json.RawMessage(`{
				"type": "m.login.password",
				"identifier": {
					"type": "m.id.user",
					"user": "@test_login_user:hs1"
				},
				"password": "superuser",
				"device_id": "`+deviceID+`"
			}`))

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("access_token", gjson.String),
					match.JSONKeyEqual("device_id", deviceID),
				},
			})
		})
		// sytest: POST /login can log in as a user with just the local part of the id
		t.Run("POST /login can log in as a user with just the local part of the id", func(t *testing.T) {
			t.Parallel()

			res := unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "login"}, json.RawMessage(`{
				"type": "m.login.password",
				"identifier": {
					"type": "m.id.user",
					"user": "test_login_user"
				},
				"password": "superuser"
			}`))

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("access_token", gjson.String),
					match.JSONKeyEqual("home_server", "hs1"),
				},
			})
		})
		// sytest: POST /login as non-existing user is rejected
		t.Run("POST /login as non-existing user is rejected", func(t *testing.T) {
			t.Parallel()
			res := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "login"}, client.WithRawBody(json.RawMessage(`{
				"type": "m.login.password",
				"identifier": {
					"type": "m.id.user",
					"user": "i-dont-exist"
				},
				"password": "superuser"
			}`)))
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 403,
			})
		})
		// sytest: POST /login wrong password is rejected
		t.Run("POST /login wrong password is rejected", func(t *testing.T) {
			t.Parallel()
			res := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "login"}, client.WithRawBody(json.RawMessage(`{
				"type": "m.login.password",
				"identifier": {
					"type": "m.id.user",
					"user": "@test_login_user:hs1"
				},
				"password": "wrong_password"
			}`)))
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 403,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_FORBIDDEN"),
				},
			})
		})

		// Regression test for https://github.com/matrix-org/dendrite/issues/2287
		t.Run("Login with uppercase username works and syncing afterwards also", func(t *testing.T) {
			t.Parallel()
			// login should be possible with uppercase username
			res := unauthedClient.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "login"}, client.WithRawBody(json.RawMessage(`{
				"type": "m.login.password",
				"identifier": {
					"type": "m.id.user",
					"user": "@Test_login_user:hs1"
				},
				"password": "superuser"
			}`)))
			// extract access_token
			body, err := ioutil.ReadAll(res.Body)
			if err != nil {
				t.Fatalf("unable to parse response body: %+v", err)
			}
			defer res.Body.Close()
			js := gjson.ParseBytes(body)
			unauthedClient.UserID = js.Get("user_id").Str
			unauthedClient.AccessToken = js.Get("access_token").Str
			// syncing should now be possible
			unauthedClient.MustSync(t, client.SyncReq{})
		})
	})
}
