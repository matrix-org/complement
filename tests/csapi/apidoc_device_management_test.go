package csapi_tests

import (
	"fmt"
	"net/http"
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
				"user": authedClient.UserID,
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
				"user": authedClient.UserID,
			},
			"password":                    "superuser",
			"device_id":                   deviceIDSecond,
			"initial_device_display_name": "device display",
		})
		_ = unauthedClient.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "login"}, reqBody)

		wantDeviceIDs := map[string]bool{
			deviceID:       true,
			deviceIDSecond: true,
		}
		res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "devices"})
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

// Alice and Bob are in a room, each on their own homeserver.
// Alice repeatedly creates a new device.
// For each new device, we check that
//  - Bob's sync response indicates that Alice's device list has changed.
//  - If Bob fetches Alice's keys then he sees the entire list.
// REVIEW: should this be here or in a new file?
func TestManyDevices(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs2", "@bob:hs2")

	loginAliceOnNewDevice := func(i int) *http.Response {
		alicePassword := "complement_meets_min_pasword_req_alice"
		return alice.MustDoFunc(
			t,
			"POST",
			[]string{"_matrix", "client", "v3", "login"},
			client.WithJSONBody(t, map[string]interface{}{
				"device_id":                   fmt.Sprintf("alice_%d", i),
				"initial_device_display_name": fmt.Sprintf("Alice device %d", i),
				"identifier": map[string]interface{}{
					"type": "m.id.user",
					"user": "alice",
				},
				"password": alicePassword,
				"type":     "m.login.password",
			},
			))
	}

	for i := 1; i <= 500; i += 1 {
		// Alice logs in to make a new device.
		loginAliceOnNewDevice(i)

		// We wait for Bob to see that Alice's device list has changed
		bob.MustSyncUntil(
			t,
			client.SyncReq{},
			client.SyncUserHasChangedDevices(alice.UserID),
		)
		// Bob fetches Alice's device list
	}
}
