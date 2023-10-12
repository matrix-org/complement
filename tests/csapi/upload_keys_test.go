package csapi_tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
)

func TestUploadKey(t *testing.T) {
	deployment := complement.Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	deviceKeys, oneTimeKeys := alice.MustGenerateOneTimeKeys(t, 1)

	t.Run("Parallel", func(t *testing.T) {
		// sytest: Can upload device keys
		t.Run("Can upload device keys", func(t *testing.T) {
			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"device_keys":   deviceKeys,
				"one_time_keys": oneTimeKeys,
			})
			resp := alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "upload"}, reqBody)
			must.MatchResponse(t, resp, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONMapEach("one_time_key_counts", func(k, v gjson.Result) error {
						keyCount := 0
						for key := range oneTimeKeys {
							// check that the returned algorithms -> key count matches those we uploaded
							if strings.HasPrefix(key, k.Str) {
								keyCount++
							}
						}
						if int(v.Float()) != keyCount {
							return fmt.Errorf("expected %d one time keys, got %d", keyCount, int(v.Float()))
						}
						return nil
					}),
				},
			})
		})

		// sytest: Rejects invalid device keys
		t.Run("Rejects invalid device keys", func(t *testing.T) {
			runtime.SkipIf(t, runtime.Dendrite, runtime.Synapse) // Blacklisted on Synapse, Dendrite FIXME: https://github.com/matrix-org/dendrite/issues/2804
			t.Parallel()
			// algorithms, keys and signatures are required fields, but missing
			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"device_keys": map[string]interface{}{
					"user_id":   bob.UserID,
					"device_id": bob.DeviceID,
				},
				"one_time_keys": oneTimeKeys,
			})
			resp := bob.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "upload"}, reqBody)
			must.MatchResponse(t, resp, match.HTTPResponse{
				StatusCode: http.StatusBadRequest,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_BAD_JSON"),
				},
			})
		})

		// sytest: Should reject keys claiming to belong to a different user
		t.Run("Should reject keys claiming to belong to a different user", func(t *testing.T) {
			runtime.SkipIf(t, runtime.Synapse) // Blacklisted
			t.Parallel()
			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"device_keys": map[string]interface{}{
					"user_id":   alice.UserID,
					"device_id": alice.DeviceID,
				},
			})
			resp := bob.Do(t, "POST", []string{"_matrix", "client", "v3", "keys", "upload"}, reqBody)
			must.MatchResponse(t, resp, match.HTTPResponse{
				StatusCode: http.StatusBadRequest,
			})
		})

		// sytest: Can query device keys using POST
		t.Run("Can query device keys using POST", func(t *testing.T) {
			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"device_keys": map[string][]string{
					alice.UserID: {},
				},
			})
			resp := alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "query"}, reqBody)
			must.MatchResponse(t, resp, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("device_keys."+client.GjsonEscape(alice.UserID)+"."+client.GjsonEscape(alice.DeviceID), gjson.JSON),
				},
			})
		})
		// sytest: Can query specific device keys using POST
		t.Run("Can query specific device keys using POST", func(t *testing.T) {
			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"device_keys": map[string][]string{
					alice.UserID: {alice.DeviceID},
				},
			})
			resp := alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "query"}, reqBody)
			deviceKeysField := "device_keys." + client.GjsonEscape(alice.UserID) + "." + client.GjsonEscape(alice.DeviceID)
			must.MatchResponse(t, resp, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONKeyTypeEqual(deviceKeysField, gjson.JSON),
					match.JSONKeyEqual(deviceKeysField+".algorithms", deviceKeys["algorithms"]),
					match.JSONKeyEqual(deviceKeysField+".device_id", deviceKeys["device_id"]),
					match.JSONKeyEqual(deviceKeysField+".keys", deviceKeys["keys"]),
					match.JSONKeyEqual(deviceKeysField+".signatures", deviceKeys["signatures"]),
				},
			})
		})
		// sytest: query for user with no keys returns empty key dict
		t.Run("query for user with no keys returns empty key dict", func(t *testing.T) {
			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"device_keys": map[string][]string{
					bob.UserID: {},
				},
			})
			resp := alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "query"}, reqBody)
			deviceKeysField := "device_keys." + client.GjsonEscape(bob.UserID)
			must.MatchResponse(t, resp, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONKeyTypeEqual(deviceKeysField, gjson.JSON),
					match.JSONKeyMissing(deviceKeysField + "." + client.GjsonEscape(bob.UserID)),
				},
			})
		})

		// sytest: Can claim one time key using POST
		t.Run("Can claim one time key using POST", func(t *testing.T) {
			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"one_time_keys": map[string]interface{}{
					alice.UserID: map[string]string{
						alice.DeviceID: "signed_curve25519",
					},
				},
			})
			resp := alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "claim"}, reqBody)
			otksField := "one_time_keys." + client.GjsonEscape(alice.UserID) + "." + client.GjsonEscape(alice.DeviceID)
			must.MatchResponse(t, resp, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONKeyTypeEqual(otksField, gjson.JSON),
					match.JSONKeyEqual(otksField, oneTimeKeys),
				},
			})

			// there should be no OTK left now
			resp = alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "claim"}, reqBody)
			must.MatchResponse(t, resp, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONKeyMissing("one_time_keys." + client.GjsonEscape(alice.UserID)),
				},
			})
		})
	})
}
