package tests

import (
	"encoding/json"
	"fmt"
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
			CreateDummyUser(t, unauthedClient, "login_test_user")
			res := unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "login"}, json.RawMessage(`{
				"type": "m.login.password",
				"identifier": {
					"type": "m.id.user",
					"user": "@login_test_user:hs1"
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
			CreateDummyUser(t, unauthedClient, "device_id_test_user")
			res := unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "login"}, json.RawMessage(`{
				"type": "m.login.password",
				"identifier": {
					"type": "m.id.user",
					"user": "@device_id_test_user:hs1"
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

			CreateDummyUser(t, unauthedClient, "local-login-user")

			res := unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "login"}, json.RawMessage(`{
				"type": "m.login.password",
				"identifier": {
					"type": "m.id.user",
					"user": "local-login-user"
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
			CreateDummyUser(t, unauthedClient, "login_wrong_password")
			res := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "login"}, client.WithRawBody(json.RawMessage(`{
				"type": "m.login.password",
				"identifier": {
					"type": "m.id.user",
					"user": "login_wrong_password"
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
	})
}

func CreateDummyUser(t *testing.T, unauthedClient *client.CSAPI, userID string) {
	reqBody, err := json.Marshal(map[string]interface{}{
		"auth": map[string]string{
			"type": "m.login.dummy",
		},
		"username": userID,
		"password": "superuser",
	})
	if err != nil {
		t.Fatalf("unable to marshal json: %v", err)
	}
	res := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "register"}, client.WithRawBody(json.RawMessage(reqBody)))
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyTypeEqual("access_token", gjson.String),
			match.JSONKeyTypeEqual("user_id", gjson.String),
		},
	})
}
