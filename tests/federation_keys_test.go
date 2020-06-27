package tests

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// TODO:
// Federation key API can act as a notary server via a $method request
// Key notary server should return an expired key if it can't find any others
// Key notary server must not overwrite a valid key with a spurious result from the origin server

// Test that a server can receive /keys requests:
// https://matrix.org/docs/spec/server_server/latest#get-matrix-key-v2-server-keyid
func TestInboundFederationKeys(t *testing.T) {
	deployment := Deploy(t, "federation_keys", b.BlueprintCleanHS)
	defer deployment.Destroy(t)
	t.Run("Federation key API allows unsigned requests for keys", func(t *testing.T) {
		fedClient := &http.Client{
			Timeout:   10 * time.Second,
			Transport: &docker.RoundTripper{deployment},
		}
		res, err := fedClient.Get("https://hs1/_matrix/key/v2/server")
		must.NotError(t, "failed to GET /keys", err)
		var key ed25519.PublicKey
		var keyID string
		body := must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyPresent("old_verify_keys"),
				match.JSONKeyEqual("server_name", "hs1"),
				match.JSONMapEach("verify_keys", func(k, v gjson.Result) error {
					// Currently we always expect ed25519 keys
					if !strings.HasPrefix(k.Str, "ed25519:") {
						return fmt.Errorf("complement: unknown key ID type, only ed25519 is supported, got '%s'", k.Str)
					}
					keyBytes, err := base64.RawStdEncoding.DecodeString(v.Get("key").Str)
					if err != nil {
						return fmt.Errorf("Failed to decode ed25519 key as base64: %s", err)
					}
					// slightly evil to side-effect like this, but it's the easiest way
					keyID = k.Str
					key = keyBytes
					return nil
				}),
			},
		})
		jsonObj := gjson.ParseBytes(body)
		gotTime := time.Unix(0, jsonObj.Get("valid_until_ts").Int()*1000*1000) // ms -> ns
		wantTime := time.Now()
		if gotTime.Before(wantTime) {
			t.Errorf("valid_until_ts : timestamp is in the past: %s < %s", gotTime, wantTime)
		}

		if keyID == "" {
			t.Fatalf("missing ed25519: key")
		}
		sigBase64 := jsonObj.Get(fmt.Sprintf("signatures.hs1.%s", keyID))
		if !sigBase64.Exists() {
			t.Fatalf("missing signature for hs1.%s", keyID)
		}
		sigBytes, err := base64.RawStdEncoding.DecodeString(sigBase64.Str)

		bodyWithoutSig, err := sjson.DeleteBytes(body, "signatures")
		if err != nil {
			t.Fatalf("failed to delete 'signatures' key: %s", err)
		}
		if !ed25519.Verify(key, bodyWithoutSig, sigBytes) {
			t.Fatalf("message was not signed by server: %s", string(bodyWithoutSig))
		}
	})
}
