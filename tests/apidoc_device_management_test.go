package tests

import (
	"io/ioutil"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestDeviceManagement(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	unauthedClient := deployment.Client(t, "hs1", "")
	authedClient := deployment.RegisterUser(t, "hs1", "test_device_management_user", "superuser")

	// sytest: GET /device/{deviceId}
	t.Run("GET /device/{deviceId}", func(t *testing.T) {
		deviceID := "login_device"
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"type": "m.login.password",
			"identifier": map[string]interface{}{
				"type": "m.id.user",
				"user": "@test_device_management_user:hs1",
			},
			"password":                    "superuser",
			"device_id":                   deviceID,
			"initial_device_display_name": "device display",
		})
		_ = unauthedClient.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "login"}, reqBody)

		res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "devices", deviceID})

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("device_id", deviceID),
				match.JSONKeyEqual("display_name", "device display"),
			},
		})
	})

	// sytest: GET /device/{deviceId} gives a 404 for unknown devices
	t.Run("GET /device/{deviceId} gives a 404 for unknown devices", func(t *testing.T) {

		res := authedClient.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "devices", "unknown_device"})

		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
		})
	})

	// sytest:	GET /devices
	t.Run("GET /devices", func(t *testing.T) {
		deviceID := "login_device"
		deviceIDSecond := "login_device_2"
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"type": "m.login.password",
			"identifier": map[string]interface{}{
				"type": "m.id.user",
				"user": "@test_device_management_user:hs1",
			},
			"password":                    "superuser",
			"device_id":                   deviceIDSecond,
			"initial_device_display_name": "device display",
		})
		_ = unauthedClient.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "login"}, reqBody)

		res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "devices"})

		foundDeviceId := 0

		// Complement also logged the login device used to create the authedClient, hence the use of the following hack

		jsonBody, err := ioutil.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("unable to read response body: %v", err)
		}

		deviceKey := gjson.Get(string(jsonBody), "devices.#.device_id")

		for _, devicesID := range deviceKey.Array() {
			if devicesID.Str == deviceID || devicesID.Str == deviceIDSecond {
				foundDeviceId++
			}
		}

		if foundDeviceId != 2 {
			t.Errorf("Unable to find device_id in the response " + string(jsonBody))
		}
	})

	// sytest:	PUT /device/{deviceId} updates device fields
	t.Run("PUT /device/{deviceId} updates device fields", func(t *testing.T) {
		deviceID := "login_device"
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"display_name": "new device display",
		})
		_ = authedClient.MustDoFunc(t, "PUT", []string{"_matrix", "client", "r0", "devices", deviceID}, reqBody)

		res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "devices", deviceID})

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("device_id", deviceID),
				match.JSONKeyEqual("display_name", "new device display"),
			},
		})
	})

	// sytest:	PUT /device/{deviceId} gives a 404 for unknown devices
	t.Run("PUT /device/{deviceId} gives a 404 for unknown devices", func(t *testing.T) {
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"display_name": "new device display",
		})
		res := authedClient.DoFunc(t, "PUT", []string{"_matrix", "client", "r0", "devices", "unknown_device"}, reqBody)

		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
		})
	})
}
