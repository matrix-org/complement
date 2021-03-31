package tests

import (
	"encoding/json"
	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
	"io/ioutil"
	"testing"
)

func TestLogin(t *testing.T) {
	deployment := Deploy(t, "login", b.BlueprintAlice)
	defer deployment.Destroy(t)
	unauthedClient := deployment.Client(t, "hs1", "")
	t.Run("parallel", func(t *testing.T) {
		// sytest: POST /login can log in as a user
		t.Run("POST /login can login as user", func(t *testing.T) {
			t.Parallel()
			res := unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "register"}, json.RawMessage(`{
				"auth": {
					"type": "m.login.dummy"
				},
				"username": "post-login-user",
				"password": "superuser"
			}`))
			body, _ := ioutil.ReadAll(res.Body)
			userId := gjson.Get(string(body), "user_id")
			res = unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "login"}, json.RawMessage(`
			{
				"type": "m.login.password"
				"identifier": {
					"type": "m.id.user",
					"user": "post-login-user",
				}
				"password": "superuser"
			}`))

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("access_token", gjson.String),
					match.JSONKeyEqual("server_name", "hs1"),
				},
			})
		})
	})
}
