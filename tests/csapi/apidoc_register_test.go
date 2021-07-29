package csapi_tests

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

// TODO:
// POST /r0/admin/register with shared secret
// POST /r0/admin/register admin with shared secret
// POST /r0/admin/register with shared secret downcases capitals
// POST /r0/admin/register with shared secret disallows symbols
// POST /r0/register rejects invalid utf-8 in JSON
// Register with a recaptcha
// registration is idempotent, without username specified
// registration is idempotent, with username specified
// registration remembers parameters
// registration accepts non-ascii passwords
// registration with inhibit_login inhibits login
// User signups are forbidden from starting with '_'
// Can register using an email address

func TestRegistration(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	unauthedClient := deployment.Client(t, "hs1", "")
	t.Run("parallel", func(t *testing.T) {
		// sytest: POST {} returns a set of flows
		t.Run("POST {} returns a set of flows", func(t *testing.T) {
			t.Parallel()
			res := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "register"}, client.WithRawBody(json.RawMessage(`{}`)))
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 401,
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
				JSON: []match.JSON{
					match.JSONArrayEach("flows", func(val gjson.Result) error {
						if !val.Get("stages").IsArray() {
							return fmt.Errorf("'stages' is not an array: %v", val.Get("stages").Raw)
						}
						return nil
					}),
				},
			})
		})
		// sytest: POST /register can create a user
		t.Run("POST /register can create a user", func(t *testing.T) {
			t.Parallel()
			res := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "register"}, client.WithRawBody(json.RawMessage(`{
				"auth": {
					"type": "m.login.dummy"
				},
				"username": "post-can-create-a-user",
				"password": "sUp3rs3kr1t"
			}`)))
			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("access_token", gjson.String),
					match.JSONKeyTypeEqual("user_id", gjson.String),
				},
			})
		})
		// sytest: POST /register downcases capitals in usernames
		t.Run("POST /register downcases capitals in usernames", func(t *testing.T) {
			t.Parallel()
			res := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "register"}, client.WithRawBody(json.RawMessage(`{
				"auth": {
					"type": "m.login.dummy"
				},
				"username": "user-UPPER",
				"password": "sUp3rs3kr1t"
			}`)))
			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("access_token", gjson.String),
					match.JSONKeyEqual("user_id", "@user-upper:hs1"),
				},
			})
		})
		// sytest: POST /register returns the same device_id as that in the request
		t.Run("POST /register returns the same device_id as that in the request", func(t *testing.T) {
			t.Parallel()
			deviceID := "my_device_id"
			res := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "register"}, client.WithRawBody(json.RawMessage(`{
				"auth": {
					"type": "m.login.dummy"
				},
				"username": "user-device",
				"password": "sUp3rs3kr1t",
				"device_id": "`+deviceID+`"
			}`)))
			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("access_token", gjson.String),
					match.JSONKeyEqual("device_id", deviceID),
				},
			})
		})
		// sytest: POST /register rejects usernames with special characters
		t.Run("POST /register rejects usernames with special characters", func(t *testing.T) {
			t.Parallel()
			specialChars := []string{
				`!`,
				`"`,
				`:`,
				`?`,
				`\\`,
				`@`,
				`[`,
				`]`,
				`{`,
				`|`,
				`}`,
				`£`,
				`é`,
				`\n`,
				`'`,
			}
			for _, ch := range specialChars {
				res := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "register"},
					client.WithJSONBody(t, map[string]interface{}{
						"auth": map[string]string{
							"type": "m.login.dummy",
						},
						"username": "user-" + ch + "-reject-please",
						"password": "sUp3rs3kr1t",
					}))
				must.MatchResponse(t, res, match.HTTPResponse{
					StatusCode: 400,
					JSON: []match.JSON{
						match.JSONKeyEqual("errcode", "M_INVALID_USERNAME"),
					},
				})
			}
		})
		t.Run("POST /register rejects if user already exists", func(t *testing.T) {
			t.Parallel()
			res := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "register"}, client.WithRawBody(json.RawMessage(`{
				"auth": {
					"type": "m.login.dummy"
				},
				"username": "post-can-create-a-user-once",
				"password": "sUp3rs3kr1t"
			}`)))
			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("access_token", gjson.String),
					match.JSONKeyTypeEqual("user_id", gjson.String),
				},
			})
			res = unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "register"}, client.WithRawBody(json.RawMessage(`{
				"auth": {
					"type": "m.login.dummy"
				},
				"username": "post-can-create-a-user-once",
				"password": "anotherSuperSecret"
			}`)))
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 400,
			})
		})
	})
}
