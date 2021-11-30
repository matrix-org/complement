package tests

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

// TODO:
// Federation key API can act as a notary server via a $method request
// Key notary server should return an expired key if it can't find any others
// Key notary server must not overwrite a valid key with a spurious result from the origin server

// Test that a server can receive /keys requests:
// https://matrix.org/docs/spec/server_server/latest#get-matrix-key-v2-server-keyid
// sytest: Federation key API allows unsigned requests for keys
func TestInboundFederationKeys(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	fedClient := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &docker.RoundTripper{Deployment: deployment},
	}

	res, err := fedClient.Get("https://hs1/_matrix/key/v2/server")
	must.NotError(t, "failed to GET /keys", err)

	var keys = map[string]ed25519.PublicKey{}
	var oldKeys = map[string]ed25519.PublicKey{}

	body := must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 200,
		JSON: []match.JSON{
			match.JSONKeyTypeEqual("valid_until_ts", gjson.Number),
			match.JSONKeyEqual("server_name", "hs1"),

			// Check validity of verify_keys+old_verify_keys and store values
			match.JSONMapEach("verify_keys", func(k, v gjson.Result) error {
				// Currently we always expect ed25519 keys
				if !strings.HasPrefix(k.Str, "ed25519:") {
					return fmt.Errorf("complement: Key '%s' has no 'ed25519:' prefix", k.Str)
				}

				key := v.Get("key")

				// Test key existence and string type
				if !key.Exists() {
					return fmt.Errorf("verify_keys: Key '%s' has no expired_ts in value", k.Str)
				}
				if key.Type != gjson.String {
					return fmt.Errorf("verify_keys: Key '%s' has expired_ts with unexpected type, expected String, got '%s'", k.Str, key.Type.String())
				}

				var keyBytes []byte
				keyBytes, err = base64.RawStdEncoding.DecodeString(key.Str)
				if err != nil {
					return fmt.Errorf("verify_keys: Key '%s' failed to decode as ed25519 base64: %s", k.Str, err)
				}

				keys[k.Str] = keyBytes
				return nil
			}),
			match.JSONMapEach("old_verify_keys", func(k, v gjson.Result) error {
				// Check for ed25519 prefix
				if !strings.HasPrefix(k.Str, "ed25519:") {
					return fmt.Errorf("old_verify_keys: Key '%s' has no 'ed25519:' prefix", k.Str)
				}

				expiredTs := v.Get("expired_ts")

				// Test expired_ts existence and number type
				if !expiredTs.Exists() {
					return fmt.Errorf("old_verify_keys: Key '%s' has no expired_ts in value", k.Str)
				}
				if expiredTs.Type != gjson.Number {
					return fmt.Errorf("old_verify_keys: Key '%s' has expired_ts with unexpected type, expected Number, got '%s'", k.Str, expiredTs.Type.String())
				}

				key := v.Get("key")

				// Test key existence and string type
				if !key.Exists() {
					return fmt.Errorf("old_verify_keys: Key '%s' has no expired_ts in value", k.Str)
				}
				if key.Type != gjson.String {
					return fmt.Errorf("old_verify_keys: Key '%s' has expired_ts with unexpected type, expected String, got '%s'", k.Str, key.Type.String())
				}

				var keyBytes []byte
				keyBytes, err = base64.RawStdEncoding.DecodeString(key.Str)
				if err != nil {
					return fmt.Errorf("old_verify_keys: Key '%s' failed to decode as ed25519 base64: %s", k.Str, err)
				}

				oldKeys[k.Str] = keyBytes

				return nil
			}),

			// Check signatures
			match.JSONMapEach("signatures", func(k, v gjson.Result) error {
				if !v.IsObject() {
					return fmt.Errorf("signatures: Value for server '%s' is not an object", k.Str)
				}

				for key, v := range v.Map() {
					// Check for ed25519 prefix
					if !strings.HasPrefix(key, "ed25519:") {
						return fmt.Errorf("signatures: Key '%s' for server '%s' has no 'ed25519:' prefix", key, k.Str)
					}

					if v.Type != gjson.String {
						return fmt.Errorf("signatures: Key '%s' for server '%s' has unexpected type, expected String, got '%s'", key, k.Str, v.Type.String())
					}

					_, err = base64.RawStdEncoding.DecodeString(v.Str)
					if err != nil {
						return fmt.Errorf("old_verify_keys: Key '%s' failed to decode as ed25519 base64: %s", k.Str, err)
					}
				}

				return nil
			}),
		},
	})

	jsonObj := gjson.ParseBytes(body)
	gotTime := time.Unix(0, jsonObj.Get("valid_until_ts").Int()*1000*1000) // ms -> ns
	wantTime := time.Now()
	if gotTime.Before(wantTime) {
		t.Errorf("valid_until_ts: timestamp is in the past: %s < %s", gotTime, wantTime)
	}

	if len(keys) == 0 {
		t.Fatalf("verify_keys: missing any ed25519: key")
	}

	if len(oldKeys) > 0 {
		// Check if any old key exists in the new keys
		for k := range oldKeys {
			if _, ok := keys[k]; ok {
				t.Fatalf("old_verify_keys: Key '%s' exists in both old_verify_keys and verify_keys", k)
			}
		}
	}

	sigObj := jsonObj.Get("signatures").Map()

	// Test signatures object sanity
	if _, ok := sigObj["hs1"]; !ok {
		t.Fatalf("signatures: Did not contain server key for 'hs1'")
	}

	if len(sigObj) > 1 {
		var extraKeys []string
		for k := range sigObj {
			if k == "hs1" {
				continue
			}
			extraKeys = append(extraKeys, k)
		}
		t.Fatalf("signatures: Contained more than one server keys, extra keys: %s", strings.Join(extraKeys, ", "))
	}

	sigServerObj := sigObj["hs1"].Map()

	type sigWithKey struct {
		signature []byte
		key       ed25519.PublicKey
		old       bool
	}

	var signatures = map[string]sigWithKey{}

	// Test signatures for all verify_keys, these *have* to exist.
	for keyName, keyBytes := range keys {
		sigBase64 := jsonObj.Get(fmt.Sprintf("signatures.hs1.%s", keyName))
		if !sigBase64.Exists() {
			t.Fatalf("signatures: missing signature for Key '%s'", keyName)
		}

		if sigBase64.Type != gjson.String {
			t.Fatalf("signatures: Signature for Key '%s' has unexpected type, expected String, got '%s'", keyName, sigBase64.Type.String())
		}

		var sigBytes []byte
		sigBytes, err = base64.RawStdEncoding.DecodeString(sigBase64.Str)
		if err != nil {
			t.Fatalf("signatures: Signature for key '%s' failed to decode as ed25519 base64: %s", keyName, err)
		}

		signatures[keyName] = sigWithKey{key: keyBytes, signature: sigBytes, old: false}
	}

	// Check if there's any leftover signatures, add them if they exist in the expired keys
	for keyName, sig := range sigServerObj {
		if _, ok := signatures[keyName]; ok {
			continue
		}

		// Found a signature that was leftover, this *should* be an expired key, if not, abort.
		keyBytes, ok := oldKeys[keyName]
		if !ok {
			t.Fatalf("signatures: Unknown signature for key '%s'", keyName)
		}

		if sig.Type != gjson.String {
			t.Fatalf("signatures: Signature for Old Key '%s' has unexpected type, expected String, got '%s'", keyName, sig.Type.String())
		}

		var sigBytes []byte
		sigBytes, err = base64.RawStdEncoding.DecodeString(sig.Str)
		if err != nil {
			t.Fatalf("signatures: Signature for Old key '%s' failed to decode as ed25519 base64: %s", keyName, err)
		}

		signatures[keyName] = sigWithKey{key: keyBytes, signature: sigBytes, old: true}
	}

	var bodyWithoutSig []byte
	bodyWithoutSig, err = sjson.DeleteBytes(body, "signatures")
	if err != nil {
		t.Fatalf("failed to delete 'signatures' key: %s", err)
	}

	for keyName, val := range signatures {
		if !ed25519.Verify(val.key, bodyWithoutSig, val.signature) {
			var oldClause = ""
			if val.old {
				oldClause = "Old "
			}
			t.Fatalf("Message signature failed to verify for %sKey '%s': %s", oldClause, keyName, string(bodyWithoutSig))
		}
	}
}
