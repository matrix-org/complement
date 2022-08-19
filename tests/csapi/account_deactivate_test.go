package csapi_tests

import (
	"net/http"
	"testing"

	"fmt"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestDeactivateAccount(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	password := "superuser"
	authedClient := deployment.RegisterUser(t, "hs1", "test_deactivate_user", password, false)
	unauthedClient := deployment.Client(t, "hs1", "")

	// Ensure that the first step, in which the client queries the server's user-interactive auth flows, returns
	// at least one auth flow involving a password.
	t.Run("Password flow is available", func(t *testing.T) {
		reqBody := client.WithJSONBody(t, map[string]interface{}{})
		res := authedClient.DoFunc(t, "POST", []string{"_matrix", "client", "v3", "account", "deactivate"}, reqBody)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 401,
			JSON: []match.JSON{
				match.JSONArrayEach(
					"flows",
					func(flow gjson.Result) error {
						stages := flow.Get("stages")
						if !stages.IsArray() {
							return fmt.Errorf("flows' stages is not an array.")
						}
						foundStage := false
						for _, stage := range stages.Array() {
							if stage.Type != gjson.String {
								return fmt.Errorf("stage is not a string")
							}
							if stage.Str == "m.login.password" {
								foundStage = true
							}
						}
						if !foundStage {
							return fmt.Errorf("No m.login.password stage found.")
						}
						return nil
					},
				),
			},
		})
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
		res := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "v3", "login"}, reqBody)
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

	res := authedClient.DoFunc(t, "POST", []string{"_matrix", "client", "v3", "account", "deactivate"}, reqBody)

	return res
}
