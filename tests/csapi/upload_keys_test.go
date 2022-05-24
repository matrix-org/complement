package csapi_tests

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/matrix-org/complement/runtime"
	"github.com/tidwall/gjson"
	"maunium.net/go/mautrix/crypto/olm"
)

func TestUploadKey(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	deviceKeys, oneTimeKeys := generateKeys(t, alice, 1)
	t.Run("Parallel", func(t *testing.T) {
		// sytest: Can upload device keys
		t.Run("Can upload device keys", func(t *testing.T) {
			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"device_keys":   deviceKeys,
				"one_time_keys": oneTimeKeys,
			})
			resp := alice.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "keys", "upload"}, reqBody)
			must.MatchResponse(t, resp, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONKeyEqual("one_time_key_counts.signed_curve25519", float64(1)),
				},
			})
		})

		// sytest: Rejects invalid device keys
		t.Run("Rejects invalid device keys", func(t *testing.T) {
			runtime.SkipIf(t, runtime.Dendrite, runtime.Synapse) // Dendrite doesn't pass, Synapse has it blacklisted
			t.Parallel()
			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"device_keys": map[string]interface{}{
					"user_id":   bob.UserID,
					"device_id": bob.DeviceID,
				},
				"one_time_keys": oneTimeKeys,
			})
			resp := bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "keys", "upload"}, reqBody)
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
			resp := bob.DoFunc(t, "POST", []string{"_matrix", "client", "v3", "keys", "upload"}, reqBody)
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
			resp := alice.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "keys", "query"}, reqBody)
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
			resp := alice.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "keys", "query"}, reqBody)
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
			resp := alice.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "keys", "query"}, reqBody)
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
			resp := alice.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "keys", "claim"}, reqBody)
			otksField := "one_time_keys." + client.GjsonEscape(alice.UserID) + "." + client.GjsonEscape(alice.DeviceID)
			must.MatchResponse(t, resp, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONKeyTypeEqual(otksField, gjson.JSON),
					match.JSONKeyEqual(otksField, oneTimeKeys),
				},
			})

			// there should be no OTK left now
			resp = alice.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "keys", "claim"}, reqBody)
			must.MatchResponse(t, resp, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONKeyMissing("one_time_keys." + client.GjsonEscape(alice.UserID)),
				},
			})
		})
	})
}

func generateKeys(t *testing.T, user *client.CSAPI, otkCount uint) (map[string]interface{}, map[string]interface{}) {
	t.Helper()
	account := olm.NewAccount()
	ed25519Key, curveKey := account.IdentityKeys()

	ed25519KeyID := fmt.Sprintf("ed25519:%s", user.DeviceID)
	curveKeyID := fmt.Sprintf("curve25519:%s", user.DeviceID)

	deviceKeys := map[string]interface{}{
		"user_id":    user.UserID,
		"device_id":  user.DeviceID,
		"algorithms": []interface{}{"m.olm.v1.curve25519-aes-sha2", "m.megolm.v1.aes-sha2"},
		"keys": map[string]interface{}{
			ed25519KeyID: ed25519Key.String(),
			curveKeyID:   curveKey.String(),
		},
	}

	signature, _ := account.SignJSON(deviceKeys)

	deviceKeys["signatures"] = map[string]interface{}{
		user.UserID: map[string]interface{}{
			ed25519KeyID: signature,
		},
	}

	account.GenOneTimeKeys(otkCount)

	oneTimeKeys := map[string]interface{}{}

	for kid, key := range account.OneTimeKeys() {
		keyID := fmt.Sprintf("signed_curve25519:%s", kid)
		keyMap := map[string]interface{}{
			"key": key.String(),
		}

		signature, _ = account.SignJSON(keyMap)

		keyMap["signatures"] = map[string]interface{}{
			user.UserID: map[string]interface{}{
				ed25519KeyID: signature,
			},
		}

		oneTimeKeys[keyID] = keyMap
	}
	return deviceKeys, oneTimeKeys
}
