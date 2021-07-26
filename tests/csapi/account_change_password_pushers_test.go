// +build !dendrite_blacklist

package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"

	"github.com/tidwall/gjson"
)

func TestChangePasswordPushers(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	password1 := "superuser"
	password2 := "my_new_password"
	passwordClient := deployment.RegisterUser(t, "hs1", "test_change_password_pusher_user", password1)

	// sytest: Pushers created with a different access token are deleted on password change
	t.Run("Pushers created with a different access token are deleted on password change", func(t *testing.T) {
		sessionOptional := createSession(t, deployment, passwordClient.UserID, password1)
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"data": map[string]interface{}{
				"url": "https://dummy.url/_matrix/push/v1/notify",
			},
			"profile_tag":         "tag",
			"kind":                "http",
			"app_id":              "complement",
			"app_display_name":    "complement_display_name",
			"device_display_name": "device_display_name",
			"pushkey":             "a_push_key",
			"lang":                "en",
		})

		_ = sessionOptional.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "pushers", "set"}, reqBody)

		changePassword(t, passwordClient, password1, password2)

		pushersSize := 0

		res := passwordClient.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "pushers"})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONArrayEach("pushers", func(val gjson.Result) error {
					pushersSize++
					return nil
				}),
			},
		})
		if pushersSize != 0 {
			t.Errorf("pushers size expected to be 0, found %d", pushersSize)
		}
	})

	// sytest: Pushers created with a the same access token are not deleted on password change
	t.Run("Pushers created with a the same access token are not deleted on password change", func(t *testing.T) {
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"data": map[string]interface{}{
				"url": "https://dummy.url/_matrix/push/v1/notify",
			},
			"profile_tag":         "tag",
			"kind":                "http",
			"app_id":              "complement",
			"app_display_name":    "complement_display_name",
			"device_display_name": "device_display_name",
			"pushkey":             "a_push_key",
			"lang":                "en",
		})

		_ = passwordClient.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "pushers", "set"}, reqBody)

		changePassword(t, passwordClient, password2, password1)

		pushersSize := 0

		res := passwordClient.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "pushers"})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONArrayEach("pushers", func(val gjson.Result) error {
					pushersSize++
					return nil
				}),
			},
		})
		if pushersSize != 1 {
			t.Errorf("pushers size expected to be 1, found %d", pushersSize)
		}
	})
}
