package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"maunium.net/go/mautrix/crypto/olm"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestFederationKeyUploadQuery(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs2", "@bob:hs2")

	// Do an initial sync so that we can see the changes come down sync.
	_, nextBatchBeforeKeyUpload := bob.MustSync(t, client.SyncReq{})

	deviceKeys, oneTimeKeys := generateKeys(t, alice, 1)
	// Upload keys
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

	// sytest: Can claim remote one time key using POST
	t.Run("Can claim remote one time key using POST", func(t *testing.T) {
		// check keys on remote server
		reqBody = client.WithJSONBody(t, map[string]interface{}{
			"one_time_keys": map[string]interface{}{
				alice.UserID: map[string]string{
					alice.DeviceID: "signed_curve25519",
				},
			},
		})
		resp = bob.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "claim"}, reqBody)
		otksField := "one_time_keys." + client.GjsonEscape(alice.UserID) + "." + client.GjsonEscape(alice.DeviceID)
		must.MatchResponse(t, resp, match.HTTPResponse{
			StatusCode: http.StatusOK,
			JSON: []match.JSON{
				match.JSONKeyTypeEqual(otksField, gjson.JSON),
				match.JSONKeyEqual(otksField, oneTimeKeys),
			},
		})
		// there should be no OTK left now
		resp = bob.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "claim"}, reqBody)
		must.MatchResponse(t, resp, match.HTTPResponse{
			StatusCode: http.StatusOK,
			JSON: []match.JSON{
				match.JSONKeyMissing("one_time_keys." + client.GjsonEscape(alice.UserID)),
			},
		})
	})

	// sytest: Can query remote device keys using POST
	t.Run("Can query remote device keys using POST", func(t *testing.T) {
		// We expect the key upload to come down /sync. We need to do this so
		// that can tell the next device update actually triggers the
		// notification to go down /sync.
		nextBatch := bob.MustSyncUntil(t, client.SyncReq{Since: nextBatchBeforeKeyUpload}, func(clientUserID string, topLevelSyncJSON gjson.Result) error {
			devicesChanged := topLevelSyncJSON.Get("device_lists.changed")
			if devicesChanged.Exists() {
				for _, userID := range devicesChanged.Array() {
					if userID.Str == alice.UserID {
						return nil
					}
				}
			}
			return fmt.Errorf("no device_lists found")
		})

		displayName := "My new displayname"
		body := client.WithJSONBody(t, map[string]interface{}{
			"display_name": displayName,
		})
		alice.MustDo(t, http.MethodPut, []string{"_matrix", "client", "v3", "devices", alice.DeviceID}, body)
		// wait for bob to receive the displayname change
		bob.MustSyncUntil(t, client.SyncReq{Since: nextBatch}, func(clientUserID string, topLevelSyncJSON gjson.Result) error {
			devicesChanged := topLevelSyncJSON.Get("device_lists.changed")
			if devicesChanged.Exists() {
				for _, userID := range devicesChanged.Array() {
					if userID.Str == alice.UserID {
						return nil
					}
				}
			}
			return fmt.Errorf("no device_lists found")
		})
		reqBody = client.WithJSONBody(t, map[string]interface{}{
			"device_keys": map[string]interface{}{
				alice.UserID: []string{},
			},
		})
		resp = bob.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "query"}, reqBody)
		deviceKeysField := "device_keys." + client.GjsonEscape(alice.UserID) + "." + client.GjsonEscape(alice.DeviceID)

		must.MatchResponse(t, resp, match.HTTPResponse{
			StatusCode: http.StatusOK,
			JSON: []match.JSON{
				match.JSONKeyTypeEqual(deviceKeysField, gjson.JSON),
				match.JSONKeyEqual(deviceKeysField+".algorithms", deviceKeys["algorithms"]),
				match.JSONKeyEqual(deviceKeysField+".keys", deviceKeys["keys"]),
				match.JSONKeyEqual(deviceKeysField+".signatures", deviceKeys["signatures"]),
				match.JSONKeyEqual(deviceKeysField+".unsigned.device_display_name", displayName),
			},
		})
	})
}

func generateKeys(t *testing.T, user *client.CSAPI, otkCount uint) (deviceKeys map[string]interface{}, oneTimeKeys map[string]interface{}) {
	t.Helper()
	account := olm.NewAccount()
	ed25519Key, curveKey := account.IdentityKeys()

	ed25519KeyID := fmt.Sprintf("ed25519:%s", user.DeviceID)
	curveKeyID := fmt.Sprintf("curve25519:%s", user.DeviceID)

	deviceKeys = map[string]interface{}{
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
	oneTimeKeys = map[string]interface{}{}

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
