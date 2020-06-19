package tests

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/tidwall/gjson"
)

func TestRegistration(t *testing.T) {
	deployment := MustDeploy(t, "registration", b.BlueprintCleanHS.Name)
	defer deployment.Destroy(false)
	unauthedClient := deployment.Client(t, "hs1", "")
	t.Run("parallel", func(t *testing.T) {
		t.Run("POST {} returns a set of flows", func(t *testing.T) {
			t.Parallel()
			res, err := unauthedClient.Do(t, "POST", []string{"_matrix", "client", "r0", "register"}, json.RawMessage(`{}`))
			MustNotError(t, "POST returned error", err)
			MustHaveStatus(t, res, 401)
			// TODO: would it be any clearer to have With... style assertions on the request itself so people can assert as much
			//       or as little as they want?
			MustHaveHeader(t, res, "Content-Type", "application/json")
			body := MustParseJSON(t, res)
			j := gjson.GetBytes(body, "flows")
			j.ForEach(func(_, val gjson.Result) bool {
				if !val.Get("stages").IsArray() {
					t.Errorf("'stages' is not an array: %v", val.Get("stages").Raw)
				}
				return true
			})
		})
		t.Run("POST /register can create a user", func(t *testing.T) {
			t.Parallel()
			res := unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "register"}, json.RawMessage(`{
				"auth": {
					"type": "m.login.dummy"
				},
				"username": "post-can-create-a-user",
				"password": "sUp3rs3kr1t"
			}`))
			body := MustParseJSON(t, res)
			MustHaveJSONKey(t, body, "access_token", func(r gjson.Result) error {
				if r.Str == "" {
					return fmt.Errorf("access_token is not a string")
				}
				return nil
			})
			MustHaveJSONKey(t, body, "user_id", func(r gjson.Result) error {
				if r.Str == "" {
					return fmt.Errorf("user_id is not a string")
				}
				return nil
			})
		})
		t.Run("POST /register downcases capitals in usernames", func(t *testing.T) {
			t.Parallel()
			res := unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "register"}, json.RawMessage(`{
				"auth": {
					"type": "m.login.dummy"
				},
				"username": "user-UPPER",
				"password": "sUp3rs3kr1t"
			}`))
			body := MustParseJSON(t, res)
			MustHaveJSONKey(t, body, "access_token", func(r gjson.Result) error {
				if r.Str == "" {
					return fmt.Errorf("access_token is not a string")
				}
				return nil
			})
			MustHaveJSONKeyEqual(t, body, "user_id", "@user-upper:hs1")
		})
		t.Run("POST /register returns the same device_id as that in the request", func(t *testing.T) {
			t.Parallel()
			deviceID := "my_device_id"
			res := unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "register"}, json.RawMessage(`{
				"auth": {
					"type": "m.login.dummy"
				},
				"username": "user-device",
				"password": "sUp3rs3kr1t",
				"device_id": "`+deviceID+`"
			}`))
			body := MustParseJSON(t, res)
			MustHaveJSONKey(t, body, "access_token", func(r gjson.Result) error {
				if r.Str == "" {
					return fmt.Errorf("access_token is not a string")
				}
				return nil
			})
			MustHaveJSONKeyEqual(t, body, "device_id", deviceID)
		})
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
				reqBody, err := json.Marshal(map[string]interface{}{
					"auth": map[string]string{
						"type": "m.login.dummy",
					},
					"username": "user-" + ch + "-reject-please",
					"password": "sUp3rs3kr1t",
				})
				if err != nil {
					t.Fatalf("failed to marshal JSON request body: %s", err)
				}
				res, err := unauthedClient.Do(t, "POST", []string{"_matrix", "client", "r0", "register"}, json.RawMessage(reqBody))
				MustNotError(t, "POST returned error", err)
				MustHaveStatus(t, res, 400)
				body := MustParseJSON(t, res)
				MustHaveJSONKeyEqual(t, body, "errcode", "M_INVALID_USERNAME")
			}
		})
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
	})
}
