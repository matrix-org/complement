package csapi_tests

import (
	"crypto/hmac"
	"crypto/sha1" // nolint:gosec
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
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
// registration with inhibit_login inhibits login
// User signups are forbidden from starting with '_'
// Can register using an email address

func TestRegistration(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	unauthedClient := deployment.Client(t, "hs1", "")
	t.Run("parallel", func(t *testing.T) {
		// sytest: GET /register yields a set of flows
		// The name in Sytest is different, the test is actually doing a POST request.
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
		// sytest: POST /register rejects registration of usernames with '$q'
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
		// sytest: POST /register allows registration of usernames with '$chr'
		t.Run("POST /register allows registration of usernames with ", func(t *testing.T) {
			testChars := []rune("q3._=-/")
			for x := range testChars {
				localpart := fmt.Sprintf("chrtestuser%s", string(testChars[x]))
				t.Run(string(testChars[x]), func(t *testing.T) {
					deployment.RegisterUser(t, "hs1", localpart, "sUp3rs3kr1t", false)
				})
			}
		})
		// sytest: POST $ep_name admin with shared secret
		t.Run("POST /_synapse/admin/v1/register admin with shared secret", func(t *testing.T) {
			res := registerSharedSecret(t, unauthedClient, "adminuser", "sUp3rs3kr1t", true)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyEqual("user_id", "@adminuser:hs1"),
				},
			})
		})
		// sytest: POST $ep_name with shared secret
		t.Run("POST /_synapse/admin/v1/register with shared secret", func(t *testing.T) {
			res := registerSharedSecret(t, unauthedClient, "user-shared-secret", "sUp3rs3kr1t", false)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyEqual("user_id", "@user-shared-secret:hs1"),
				},
			})
		})
		// sytest: POST $ep_name with shared secret disallows symbols
		t.Run("POST /_synapse/admin/v1/register with shared secret disallows symbols", func(t *testing.T) {
			res := registerSharedSecret(t, unauthedClient, "us,er", "sUp3rs3kr1t", false)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 400,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_INVALID_USERNAME"),
				},
			})
		})
		// sytest: POST $ep_name with shared secret downcases capitals
		t.Run("POST /_synapse/admin/v1/register with shared secret downcases capitals", func(t *testing.T) {
			res := registerSharedSecret(t, unauthedClient, "user-UPPER-shared-SECRET", "sUp3rs3kr1t", false)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("access_token", gjson.String),
					match.JSONKeyEqual("user_id", "@user-upper-shared-secret:hs1"),
				},
			})
		})
		// sytest: registration accepts non-ascii passwords
		t.Run("Registration accepts non-ascii passwords", func(t *testing.T) {
			reqJson := map[string]interface{}{
				"username":                    "test_user",
				"password":                    "übers3kr1t",
				"device_id":                   "xyzzy",
				"initial_device_display_name": "display_name"}
			resp := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "register"}, client.WithJSONBody(t, reqJson))
			body, err := ioutil.ReadAll(resp.Body)
			session := gjson.GetBytes(body, "session")
			if err != nil {
				t.Fatalf("Failed to read response body: %s", err)
			}
			must.MatchResponse(t, resp, match.HTTPResponse{StatusCode: 401})
			auth := map[string]interface{}{
				"session": session.Str,
				"type":    "m.login.dummy",
			}
			reqBody := map[string]interface{}{
				"username":                    "test_user",
				"password":                    "übers3kr1t",
				"device_id":                   "xyzzy",
				"initial_device_display_name": "display_name",
				"auth":                        auth,
			}
			resp2 := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "register"}, client.WithJSONBody(t, reqBody))
			must.MatchResponse(t, resp2, match.HTTPResponse{JSON: []match.JSON{
				match.JSONKeyPresent("access_token"),
			}})
		})
	})
}

// registerSharedSecret tries to register using a shared secret, returns the *http.Response
func registerSharedSecret(t *testing.T, c *client.CSAPI, user, pass string, isAdmin bool) *http.Response {
	resp := c.DoFunc(t, "GET", []string{"_synapse", "admin", "v1", "register"})
	if resp.StatusCode != 200 {
		t.Skipf("Homeserver image does not support shared secret registration, /_synapse/admin/v1/register returned HTTP %d", resp.StatusCode)
		return resp
	}
	body := must.ParseJSON(t, resp.Body)
	nonce := gjson.GetBytes(body, "nonce")
	if !nonce.Exists() {
		t.Fatalf("Malformed shared secret GET response: %s", string(body))
	}
	mac := hmac.New(sha1.New, []byte(client.SharedSecret))
	mac.Write([]byte(nonce.Str))
	mac.Write([]byte("\x00"))
	mac.Write([]byte(user))
	mac.Write([]byte("\x00"))
	mac.Write([]byte(pass))
	mac.Write([]byte("\x00"))
	if isAdmin {
		mac.Write([]byte("admin"))
	} else {
		mac.Write([]byte("notadmin"))
	}
	sig := mac.Sum(nil)
	reqBody := map[string]interface{}{
		"nonce":    nonce.Str,
		"username": user,
		"password": pass,
		"mac":      hex.EncodeToString(sig),
		"admin":    isAdmin,
	}
	resp = c.DoFunc(t, "POST", []string{"_synapse", "admin", "v1", "register"}, client.WithJSONBody(t, reqBody))
	return resp
}
