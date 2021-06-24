package tests

import (
	"encoding/json"
	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"

	"net/http"
	"testing"
)

func TestDeviceManagement(t *testing.T){
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	unauthedClient := deployment.Client(t, "hs1", "")
	authedClient := deployment.RegisterUser(t, "hs1", "test_device_management_user", "superuser")

	// sytest: GET /device/{deviceId}
	t.Run("GET /device/{deviceId}", func(t *testing.T) {
		deviceID := "login_device"
		_ = unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "login"}, json.RawMessage(`{
				"type": "m.login.password",
				"identifier": {
					"type": "m.id.user",
					"user": "@test_device_management_user:hs1"
				},
				"password": "superuser",
				"device_id": "`+deviceID+`",
				"initial_device_display_name": "device display"
			}`))
		res := getDevice(t, authedClient, deviceID)

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("device_id", deviceID),
				match.JSONKeyEqual("display_name", "device display"),

			},
		})
	})
}

func getDevice(t *testing.T, authedClient *client.CSAPI, deviceID string) *http.Response{
	res := authedClient.MustDo(t, "GET", []string{"_matrix", "client", "r0", "devices", deviceID}, nil)
	return res
}
