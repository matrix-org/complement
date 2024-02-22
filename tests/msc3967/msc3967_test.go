package tests

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/tidwall/gjson"
)

// MSC3967: Do not require UIA when first uploading cross signing keys
// Tests that:
// - No UIA required on POST /_matrix/client/v3/keys/device_signing/upload
// - If x-signing keys exist, does require UIA.
// - If reupload exactly the same x-signing keys, does not require UIA.
func TestMSC3967(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	privKey := ed25519.NewKeyFromSeed([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32})
	pubKey := base64.RawStdEncoding.EncodeToString(privKey.Public().(ed25519.PublicKey))
	t.Logf("pub key => %s", pubKey)
	password := "helloworld"
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		Password: password,
	})

	// uploading x-signing master key does not require UIA (so no 401)
	uploadBody := map[string]any{
		"master_key": map[string]any{
			"user_id": alice.UserID,
			"usage":   []string{"master"},
			"keys": map[string]string{
				"ed25519:" + pubKey: pubKey,
			},
		},
	}
	alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "device_signing", "upload"}, client.WithJSONBody(t, uploadBody))

	// reuploading the exact same request does not require UIA (idempotent)
	alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "device_signing", "upload"}, client.WithJSONBody(t, uploadBody))

	// trying to replace the master key requires UIA
	pubKey2, _, err := ed25519.GenerateKey(nil)
	must.NotError(t, "failed to generate 2nd key", err)
	pubKey2Base64 := base64.RawStdEncoding.EncodeToString(pubKey2)
	t.Logf("pub key 2 => %s", pubKey2Base64)
	res := alice.Do(t, "POST", []string{"_matrix", "client", "v3", "keys", "device_signing", "upload"}, client.WithJSONBody(t, map[string]any{
		"master_key": map[string]any{
			"user_id": alice.UserID,
			"usage":   []string{"master"},
			"keys": map[string]string{
				"ed25519:" + pubKey2Base64: pubKey2Base64,
			},
		},
	}))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 401,
		JSON: []match.JSON{
			match.JSONKeyPresent("flows"),
		},
	})

	// adding a key does require UIA
	ssKey, _, err := ed25519.GenerateKey(nil)
	must.NotError(t, "failed to generate self-signing key", err)
	ssKey64 := base64.RawStdEncoding.EncodeToString(ssKey)
	t.Logf("self-signing key => %s", ssKey64)
	// replace the body so no master_key in this request, because if we include it
	// Synapse will 500 as I guess you can't replace it?!
	uploadBody = map[string]any{
		"self_signing_key": map[string]any{
			"user_id": alice.UserID,
			"usage":   []string{"self_signing"},
			"keys": map[string]string{
				"ed25519:" + ssKey64: ssKey64,
			},
		},
	}
	toSign, err := json.Marshal(uploadBody["self_signing_key"])
	must.NotError(t, "failed to marshal req body", err)
	signedBody, err := gomatrixserverlib.SignJSON(alice.UserID, gomatrixserverlib.KeyID("ed25519:"+pubKey), privKey, toSign)
	must.NotError(t, "failed to sign json", err)
	uploadBody["self_signing_key"] = json.RawMessage(signedBody)
	res = alice.Do(t, "POST", []string{"_matrix", "client", "v3", "keys", "device_signing", "upload"}, client.WithJSONBody(t, uploadBody))
	body := must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 401,
		JSON: []match.JSON{
			match.JSONKeyPresent("flows"),
		},
	})

	// but if we UIA then it should work.
	uploadBody["auth"] = map[string]any{
		"type": "m.login.password",
		"identifier": map[string]any{
			"type": "m.id.user",
			"user": alice.UserID,
		},
		"password": password,
		"session":  gjson.ParseBytes(body).Get("session").Str,
	}
	alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "device_signing", "upload"}, client.WithJSONBody(t, uploadBody))

	// ensure the endpoint remains idempotent with the auth dict
	alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "device_signing", "upload"}, client.WithJSONBody(t, uploadBody))
}
