package tests

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io/ioutil"
	"strings"
	"testing"
	"time"

	"github.com/matrix-org/complement/internal/b"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type serverKeyFields struct {
	ServerName string `json:"server_name"`
	VerifyKeys map[string]struct {
		Key string `json:"key"`
	} `json:"verify_keys"`
	ValidUntilTS  int64                  `json:"valid_until_ts"`
	OldVerifyKeys map[string]interface{} `json:"old_verify_keys"`
	// server_name -> { keyID: signature }
	Signatures map[string]map[string]string
}

// Test that a server can receive /keys requests:
// https://matrix.org/docs/spec/server_server/latest#get-matrix-key-v2-server-keyid
func TestInboundFederationKeys(t *testing.T) {
	deployment := MustDeploy(t, "federation_keys", b.BlueprintCleanHS.Name)
	defer deployment.Destroy(false)
	t.Run("Federation key API allows unsigned requests for keys", func(t *testing.T) {
		fedClient := deployment.FederationClient(t, "hs1")
		res := fedClient.MustDo(t, "GET", []string{"_matrix", "key", "v2", "server"}, nil)

		body, err := ioutil.ReadAll(res.Body)
		MustNotError(t, "failed to read response body", err)
		var k serverKeyFields
		if err := json.Unmarshal(body, &k); err != nil {
			t.Fatalf("Failed to decode JSON response: %s", err)
		}
		if k.ServerName != "hs1" {
			t.Errorf("server_name : got %s want %s", k.ServerName, "hs1")
		}
		gotTime := time.Unix(0, k.ValidUntilTS*1000*1000) // ms -> ns
		wantTime := time.Now()
		if gotTime.Before(wantTime) {
			t.Errorf("valid_until_ts : timestamp is in the past: %s < %s", gotTime, wantTime)
		}
		if len(k.VerifyKeys) == 0 {
			t.Errorf("verify_keys : Expected 1 or more keys, got none")
		}
		// Currently we always expect ed25519 keys
		var key ed25519.PublicKey
		var keyID string
		for kid, verifyKey := range k.VerifyKeys {
			if strings.HasPrefix(string(kid), "ed25519:") {
				keyID = kid
				keyBytes, err := base64.RawStdEncoding.DecodeString(verifyKey.Key)
				if err != nil {
					t.Fatalf("Failed to decode ed25519 key as base64: %s", err)
				}
				key = keyBytes
				break
			}
		}
		if keyID == "" {
			t.Fatalf("missing ed25519: key")
		}
		hsSig, ok := k.Signatures["hs1"]
		if !ok {
			t.Fatalf("missing signature for own server")
		}
		sigBase64, ok := hsSig[keyID]
		if !ok {
			t.Fatalf("missing signature for key ID %s", keyID)
		}
		sigBytes, err := base64.RawStdEncoding.DecodeString(sigBase64)

		bodyWithoutSig, err := sjson.DeleteBytes(body, "signatures")
		if err != nil {
			t.Fatalf("failed to delete 'signatures' key: %s", err)
		}
		if !ed25519.Verify(key, bodyWithoutSig, sigBytes) {
			t.Fatalf("message was not signed by server: %s", string(bodyWithoutSig))
		}

		// old_verify_keys is mandatory, even if it's empty
		MustHaveJSONKey(t, body, "old_verify_keys", func(r gjson.Result) error { return nil })
	})
}
