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
	deployment := Deploy(t, "login", b.BlueprintAlice)
	defer deployment.Destroy(t)
	unauthedClient := deployment.Client(t, "hs1", "")
	t.Run("parallel", func(t *testing.T) {
		// sytest: GET /login yields a set of flows
		t.Run("GET /login yields a set of flows", func(t *testing.T) {
			t.Parallel()
			res := unauthedClient.MustDo(t, "GET", []string{"_matrix", "client", "r0", "login"}, json.RawMessage(`{}`))
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
			createDummyUser(t, unauthedClient, "login_test_user")
			res := unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "login"}, json.RawMessage(`{
				"type": "m.login.password",
				"identifier": {
					"type": "m.id.user",
					"user": "login_test_user"
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
			deviceId := "test_device_id"
			createDummyUser(t, unauthedClient, "device_id_test_user")
			res := unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "login"}, json.RawMessage(`{
				"type": "m.login.password",
				"identifier": {
					"type": "m.id.user",
					"user": "device_id_test_user"
				},
				"password": "superuser",
				"device_id": "`+deviceId+`"
			}`))

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("access_token", gjson.String),
					match.JSONKeyEqual("device_id", deviceId),
				},
			})
		})
	})
}

func createDummyUser(t *testing.T, unauthedClient *client.CSAPI, userID string) {
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
	res, err := unauthedClient.Do(t, "POST", []string{"_matrix", "client", "r0", "register"}, json.RawMessage(reqBody), nil)
	if err != nil {
		t.Fatalf("unable to make register user: %v", err)
	}
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyTypeEqual("access_token", gjson.String),
			match.JSONKeyTypeEqual("user_id", gjson.String),
		},
	})
}
