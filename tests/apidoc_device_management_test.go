package tests

import (
	"testing"

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
}
