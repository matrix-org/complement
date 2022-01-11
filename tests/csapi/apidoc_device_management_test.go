package csapi_tests

import (
	"fmt"
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
func TestAddingManyDevices(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs2", "@bob:hs2")

	loginAliceOnNewDevice := func(i int) string {
		alicePassword := "complement_meets_min_pasword_req_alice"
		deviceId := fmt.Sprintf("alice_%d", i)
		response := alice.MustDoFunc(
			t,
			"POST",
			[]string{"_matrix", "client", "v3", "login"},
			client.WithJSONBody(t, map[string]interface{}{
				"device_id":                   deviceId,
				"initial_device_display_name": fmt.Sprintf("Alice device %d", i),
				"identifier": map[string]interface{}{
					"type": "m.id.user",
					"user": "alice",
				},
				"password": alicePassword,
				"type":     "m.login.password",
			}),
		)
		body, err := ioutil.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		jsonBody := gjson.ParseBytes(body)
		must.EqualStr(t, jsonBody.Get("device_id").Str, deviceId, "wrong device id")
		return jsonBody.Get("access_token").Str
	}

	uploadKeysForDevice := func(i int, accessToken string) {
		alice.AccessToken = accessToken // urgh
		alice.MustDoFunc(
			t,
			"POST",
			[]string{"_matrix", "client", "v3", "keys", "upload"},
			client.WithJSONBody(t, map[string]interface{}{
				"device_keys": map[string]interface{}{
					"algorithms": []string{},
					"device_id":  fmt.Sprintf("alice_%d", i),
					"keys":       map[string]interface{}{},
					"signatures": map[string]interface{}{},
					"user_id":    alice.UserID,
				},
			}),
		)
	}

	fetchAliceDeviceList := func(i int) map[string]gjson.Result {
		response := bob.MustDoFunc(
			t,
			"POST",
			[]string{"_matrix", "client", "v3", "keys", fmt.Sprintf("query?i=%d", i)},
			client.WithJSONBody(t, map[string]interface{}{
				"device_keys": map[string]interface{}{
					alice.UserID: []string{},
				},
			}),
		)
		body, err := ioutil.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		t.Log(string(body))
		return gjson.ParseBytes(body).Get("device_keys").Get(alice.UserID).Map()
	}

	bobSyncConfig := client.SyncReq{}
	for i := 1; i <= 500; i += 1 {
		// Alice logs in to make a new device.
		aliceAccessToken := loginAliceOnNewDevice(i)
		uploadKeysForDevice(i, aliceAccessToken)

		success := false
		for queries := 0; queries < 2; queries += 1 {
			// We wait for Bob to see that Alice's device list has changed
			bobSyncConfig.Since = bob.MustSyncUntil(
				t,
				bobSyncConfig,
				client.SyncUserHasChangedDevices(alice.UserID),
			)

			// Check to see that we see the expected number of devices.
			// We have to check up to twice because the remote HS sends two EDUs:
			// one for the device display name, and one for the keys. Bob might
			// query after receiving the first but not the second.
			devices := fetchAliceDeviceList(i)
			if len(devices) != i {
				t.Logf("got %d devices; expected %d", len(devices), i)
				t.Logf("%s", devices)
			} else {
				success = true
				break
			}
		}

		if !success {
			t.FailNow()
		}
	}
}
