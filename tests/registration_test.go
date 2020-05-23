package tests

import (
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
	})
	deployment.Destroy()
}
