package tests

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
	"maunium.net/go/mautrix/crypto/olm"
)

func TestOneTimeKeys(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs2", "@bob:hs2")

	deviceKeys, oneTimeKeys := generateKeys(t, alice, 1)
	t.Run("Parallel", func(t *testing.T) {
		// sytest: Can claim remote one time key using POST
		t.Run("Can claim remote one time key using POST", func(t *testing.T) {
			// Upload keys
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

			// check keys on remote server
			reqBody = client.WithJSONBody(t, map[string]interface{}{
				"one_time_keys": map[string]interface{}{
					alice.UserID: map[string]string{
						alice.DeviceID: "signed_curve25519",
					},
				},
			})
			resp = bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "keys", "claim"}, reqBody)
			otksField := "one_time_keys." + client.GjsonEscape(alice.UserID) + "." + client.GjsonEscape(alice.DeviceID)
			must.MatchResponse(t, resp, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONKeyTypeEqual(otksField, gjson.JSON),
					match.JSONKeyEqual(otksField, oneTimeKeys),
				},
			})

			// there should be no OTK left now
			resp = bob.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "keys", "claim"}, reqBody)
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
