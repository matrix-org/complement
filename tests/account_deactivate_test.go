package tests

import (
	"net/http"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestDeactivateAccount(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	password := "superuser"
	authedClient := deployment.RegisterUser(t, "hs1", "test_deactivate_user", password)
	unauthedClient := deployment.Client(t, "hs1", "")
	// sytest: Can deactivate account
	t.Run("Can deactivate account", func(t *testing.T) {

		res := deactivateAccount(t, authedClient, password)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
		})
	})
	// sytest: Can't deactivate account with wrong password
	t.Run("Can't deactivate account with wrong password", func(t *testing.T) {

		res := deactivateAccount(t, authedClient, password)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 401,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_FORBIDDEN"),
			},
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
		res := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "login"}, reqBody)
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

	res := authedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "account", "deactivate"}, reqBody)

	return res
}
