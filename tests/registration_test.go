package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/tidwall/gjson"
)

func TestRegistration(t *testing.T) {
	deployment := MustDeploy(t, "registration", b.BlueprintCleanHS.Name)
	t.Run("parallel", func(t *testing.T) {
		t.Run("POST {} returns a set of flows", func(t *testing.T) {
			t.Parallel()
			res, err := http.Post(deployment.HS["hs1"].BaseURL+"/_matrix/client/r0/register", "application/json", strings.NewReader(`{}`))
			MustNotError(t, "POST returned error", err)
			MustHaveStatus(t, res, 401)
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
			res, err := http.Post(deployment.HS["hs1"].BaseURL+"/_matrix/client/r0/register", "application/json", strings.NewReader(`{
				"auth": {
					"type": "m.login.dummy"
				},
				"username": "post-can-create-a-user",
				"password": "sUp3rs3kr1t"
			}`))
			MustNotError(t, "POST returned error", err)
			MustHaveStatus(t, res, 200)
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
			res, err := http.Post(deployment.HS["hs1"].BaseURL+"/_matrix/client/r0/register", "application/json", strings.NewReader(`{
				"auth": {
					"type": "m.login.dummy"
				},
				"username": "user-UPPER",
				"password": "sUp3rs3kr1t"
			}`))
			MustNotError(t, "POST returned error", err)
			MustHaveStatus(t, res, 200)
			body := MustParseJSON(t, res)
			MustHaveJSONKey(t, body, "access_token", func(r gjson.Result) error {
				if r.Str == "" {
					return fmt.Errorf("access_token is not a string")
				}
				return nil
			})
			MustHaveJSONKeyEqual(t, body, "user_id", "@user-upper:localhost")
		})
		t.Run("POST /register returns the same device_id as that in the request", func(t *testing.T) {
			t.Parallel()
			deviceID := "my_device_id"
			res, err := http.Post(deployment.HS["hs1"].BaseURL+"/_matrix/client/r0/register", "application/json", strings.NewReader(`{
				"auth": {
					"type": "m.login.dummy"
				},
				"username": "user-device",
				"password": "sUp3rs3kr1t",
				"device_id": "`+deviceID+`"
			}`))
			MustNotError(t, "POST returned error", err)
			MustHaveStatus(t, res, 200)
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
				res, err := http.Post(deployment.HS["hs1"].BaseURL+"/_matrix/client/r0/register", "application/json", bytes.NewBuffer(reqBody))
				MustNotError(t, "POST returned error", err)
				MustHaveStatus(t, res, 400)
				body := MustParseJSON(t, res)
				MustHaveJSONKeyEqual(t, body, "errcode", "M_INVALID_USERNAME")
			}
		})
	})
	deployment.Destroy()
}
