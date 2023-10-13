package csapi_tests

import (
	"net/http"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

func TestDeactivateAccount(t *testing.T) {
	deployment := complement.Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	password := "superuser"
	authedClient := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		Password: password,
	})
	unauthedClient := deployment.UnauthenticatedClient(t, "hs1")

	// Ensure that the first step, in which the client queries the server's user-interactive auth flows, returns
	// at least one auth flow involving a password.
	t.Run("Password flow is available", func(t *testing.T) {
		reqBody := client.WithJSONBody(t, map[string]interface{}{})
		res := authedClient.Do(t, "POST", []string{"_matrix", "client", "v3", "account", "deactivate"}, reqBody)

		rawBody := must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 401,
		})
		body := gjson.ParseBytes(rawBody)

		// Example: {"session":"wombat","flows":[{"stages":["m.login.password"]}],"params":{}}
		t.Logf("Received JSON %s", body.String())

		flowList, ok := body.Get("flows").Value().([]interface{})
		if !ok {
			t.Fatalf("flows is not a list")
			return
		}

		foundPasswordStage := false

	outer:
		for _, flow := range flowList {
			flowObject, ok := flow.(map[string]interface{})
			stageList, ok := flowObject["stages"].([]interface{})
			if !ok {
				t.Fatalf("stages is not a list")
				return
			}

			for _, stage := range stageList {
				stageName, ok := stage.(string)
				if !ok {
					t.Fatalf("stage is not a string")
					return
				}
				if stageName == "m.login.password" {
					foundPasswordStage = true
					break outer
				}
			}
		}

		if !foundPasswordStage {
			t.Errorf("No m.login.password login stages found.")
		}
	})

	// sytest: Can't deactivate account with wrong password
	t.Run("Can't deactivate account with wrong password", func(t *testing.T) {
		res := deactivateAccount(t, authedClient, "wrong_password")
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 401,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_FORBIDDEN"),
			},
		})
	})
	// sytest: Can deactivate account
	t.Run("Can deactivate account", func(t *testing.T) {

		res := deactivateAccount(t, authedClient, password)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
		})
	})
	// sytest: After deactivating account, can't log in with password
	t.Run("After deactivating account, can't log in with password", func(t *testing.T) {

		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"identifier": map[string]interface{}{
				"type": "m.id.user",
				"user": authedClient.UserID,
			},
			"type":     "m.login.password",
			"password": password,
		})
		res := unauthedClient.Do(t, "POST", []string{"_matrix", "client", "v3", "login"}, reqBody)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 403,
		})
	})
}

func deactivateAccount(t *testing.T, authedClient *client.CSAPI, password string) *http.Response {
	t.Helper()
	reqBody := client.WithJSONBody(t, map[string]interface{}{
		"auth": map[string]interface{}{
			"type":     "m.login.password",
			"user":     authedClient.UserID,
			"password": password,
		},
	})

	res := authedClient.Do(t, "POST", []string{"_matrix", "client", "v3", "account", "deactivate"}, reqBody)

	return res
}
