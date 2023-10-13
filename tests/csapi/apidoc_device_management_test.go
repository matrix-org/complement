package csapi_tests

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

func TestDeviceManagement(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	unauthedClient := deployment.UnauthenticatedClient(t, "hs1")
	authedClient := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		Password: "superuser",
	})

	// sytest: GET /device/{deviceId}
	t.Run("GET /device/{deviceId}", func(t *testing.T) {
		deviceID := "login_device"
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"type": "m.login.password",
			"identifier": map[string]interface{}{
				"type": "m.id.user",
				"user": authedClient.UserID,
			},
			"password":                    "superuser",
			"device_id":                   deviceID,
			"initial_device_display_name": "device display",
		})
		_ = unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "v3", "login"}, reqBody)

		res := authedClient.MustDo(t, "GET", []string{"_matrix", "client", "v3", "devices", deviceID})

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("device_id", deviceID),
				match.JSONKeyEqual("display_name", "device display"),
			},
		})
	})

	// sytest: GET /device/{deviceId} gives a 404 for unknown devices
	t.Run("GET /device/{deviceId} gives a 404 for unknown devices", func(t *testing.T) {

		res := authedClient.Do(t, "GET", []string{"_matrix", "client", "v3", "devices", "unknown_device"})

		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
		})
	})

	// sytest: GET /devices
	t.Run("GET /devices", func(t *testing.T) {
		deviceID := "login_device"
		deviceIDSecond := "login_device_2"
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"type": "m.login.password",
			"identifier": map[string]interface{}{
				"type": "m.id.user",
				"user": authedClient.UserID,
			},
			"password":                    "superuser",
			"device_id":                   deviceIDSecond,
			"initial_device_display_name": "device display",
		})
		_ = unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "v3", "login"}, reqBody)

		wantDeviceIDs := map[string]bool{
			deviceID:       true,
			deviceIDSecond: true,
		}
		res := authedClient.MustDo(t, "GET", []string{"_matrix", "client", "v3", "devices"})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONArrayEach("devices", func(r gjson.Result) error {
					gotDeviceID := r.Get("device_id").Str
					if wantDeviceIDs[gotDeviceID] {
						delete(wantDeviceIDs, gotDeviceID)
						return nil
					}
					return nil
				}),
			},
		})
		if len(wantDeviceIDs) != 0 {
			t.Errorf("/devices did not return the following expected devices: %v", wantDeviceIDs)
		}
	})

	// sytest: PUT /device/{deviceId} updates device fields
	t.Run("PUT /device/{deviceId} updates device fields", func(t *testing.T) {
		deviceID := "login_device"
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"display_name": "new device display",
		})
		_ = authedClient.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "devices", deviceID}, reqBody)

		res := authedClient.MustDo(t, "GET", []string{"_matrix", "client", "v3", "devices", deviceID})

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("device_id", deviceID),
				match.JSONKeyEqual("display_name", "new device display"),
			},
		})
	})

	// sytest: PUT /device/{deviceId} gives a 404 for unknown devices
	t.Run("PUT /device/{deviceId} gives a 404 for unknown devices", func(t *testing.T) {
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"display_name": "new device display",
		})
		res := authedClient.Do(t, "PUT", []string{"_matrix", "client", "v3", "devices", "unknown_device"}, reqBody)

		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
		})
	})

	// sytest: DELETE /device/{deviceId}
	t.Run("DELETE /device/{deviceId}", func(t *testing.T) {
		newDeviceID, session2 := createSession(t, deployment, authedClient.UserID, "superuser")
		session2.MustSync(t, client.SyncReq{})

		// sytest: DELETE /device/{deviceId} with no body gives a 401
		res := authedClient.Do(t, "DELETE", []string{"_matrix", "client", "v3", "devices", newDeviceID})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 401,
			JSON: []match.JSON{
				match.JSONKeyPresent("session"),
				match.JSONKeyPresent("flows"),
				match.JSONKeyPresent("params"),
			},
		})

		// delete with wrong password
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"auth": map[string]interface{}{
				"identifier": map[string]interface{}{
					"type": "m.id.user",
					"user": authedClient.UserID,
				},
				"type":     "m.login.password",
				"password": "super-wrong-password",
			},
		})

		res = authedClient.Do(t, "DELETE", []string{"_matrix", "client", "v3", "devices", newDeviceID}, reqBody)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 401,
			JSON: []match.JSON{
				match.JSONKeyPresent("flows"),
				match.JSONKeyPresent("params"),
				match.JSONKeyPresent("session"),
				match.JSONKeyPresent("error"),
				match.JSONKeyPresent("errcode"),
				match.JSONKeyEqual("errcode", "M_FORBIDDEN"),
			},
		})

		// delete with correct password
		reqBody = client.WithJSONBody(t, map[string]interface{}{
			"auth": map[string]interface{}{
				"identifier": map[string]interface{}{
					"type": "m.id.user",
					"user": authedClient.UserID,
				},
				"type":     "m.login.password",
				"password": "superuser",
			},
		})

		res = authedClient.Do(t, "DELETE", []string{"_matrix", "client", "v3", "devices", newDeviceID}, reqBody)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
		})

		// verify device is deleted
		res = authedClient.Do(t, "GET", []string{"_matrix", "client", "v3", "devices", newDeviceID})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
		})

		// check that the accesstoken is invalidated
		res = session2.Do(t, "GET", []string{"_matrix", "client", "v3", "sync"})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 401,
		})
	})
	// sytest: DELETE /device/{deviceId} requires UI auth user to match device owner
	t.Run("DELETE /device/{deviceId} requires UI auth user to match device owner", func(t *testing.T) {
		bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{
			LocalpartSuffix: "bob",
			Password:        "bobspassword",
		})

		newDeviceID, session2 := createSession(t, deployment, authedClient.UserID, "superuser")
		session2.MustSync(t, client.SyncReq{})

		// delete with bob
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"auth": map[string]interface{}{
				"identifier": map[string]interface{}{
					"type": "m.id.user",
					"user": bob.UserID,
				},
				"type":     "m.login.password",
				"password": "bobspassword",
			},
		})

		res := authedClient.Do(t, "DELETE", []string{"_matrix", "client", "v3", "devices", newDeviceID}, reqBody)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 403,
		})

		// verify device still exists
		res = authedClient.Do(t, "GET", []string{"_matrix", "client", "v3", "devices", newDeviceID})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
		})

		// delete with first user password
		reqBody = client.WithJSONBody(t, map[string]interface{}{
			"auth": map[string]interface{}{
				"identifier": map[string]interface{}{
					"type": "m.id.user",
					"user": authedClient.UserID,
				},
				"type":     "m.login.password",
				"password": "superuser",
			},
		})

		res = authedClient.Do(t, "DELETE", []string{"_matrix", "client", "v3", "devices", newDeviceID}, reqBody)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
		})

		// verify device is deleted
		res = authedClient.Do(t, "GET", []string{"_matrix", "client", "v3", "devices", newDeviceID})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
		})
	})
}
