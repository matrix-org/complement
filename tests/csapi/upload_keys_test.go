package csapi_tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
)

func TestUploadKey(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

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

// Per MSC4225, keys must be issued in the same order they are uploaded
func TestKeyClaimOrdering(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	_, oneTimeKeys := alice.MustGenerateOneTimeKeys(t, 2)

	// first upload key 1, sleep a bit, then upload key 0.
	otk1 := map[string]interface{}{"signed_curve25519:1": oneTimeKeys["signed_curve25519:1"]}
	alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "upload"},
		client.WithJSONBody(t, map[string]interface{}{"one_time_keys": otk1}))

    // Ensure that there is a difference in timestamp between the two upload requests.
	time.Sleep(1 * time.Second)

	otk0 := map[string]interface{}{"signed_curve25519:0": oneTimeKeys["signed_curve25519:0"]}
	alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "upload"},
		client.WithJSONBody(t, map[string]interface{}{"one_time_keys": otk0}))

	// Now claim the keys, and check they come back in the right order
	reqBody := client.WithJSONBody(t, map[string]interface{}{
		"one_time_keys": map[string]interface{}{
			alice.UserID: map[string]string{
				alice.DeviceID: "signed_curve25519",
			},
		},
	})
	otksField := "one_time_keys." + client.GjsonEscape(alice.UserID) + "." + client.GjsonEscape(alice.DeviceID)
	resp := alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "claim"}, reqBody)
	must.MatchResponse(t, resp, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON:       []match.JSON{match.JSONKeyEqual(otksField, otk1)},
	})
	resp = alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "claim"}, reqBody)
	must.MatchResponse(t, resp, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON:       []match.JSON{match.JSONKeyEqual(otksField, otk0)},
	})
}

// Tests idempotency of the /keys/upload endpoint.
// Tests that if you upload 4 OTKs then upload the same 4, no error is returned.
func TestUploadKeyIdempotency(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	deviceKeys, oneTimeKeys := alice.MustGenerateOneTimeKeys(t, 4)
	requests := []client.RequestOpt{
		client.WithJSONBody(t, map[string]interface{}{
			"device_keys":   deviceKeys,
			"one_time_keys": oneTimeKeys,
		}),
		client.WithJSONBody(t, map[string]interface{}{
			"one_time_keys": oneTimeKeys,
		}),
		client.WithJSONBody(t, map[string]interface{}{
			"one_time_keys": oneTimeKeys,
		}),
	}
	for _, reqBody := range requests {
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
	}
}

// Tests idempotency of the /keys/upload endpoint.
// Tests that if you upload OTKs A,B,C then upload OTKs B,C,D, no error is returned and the OTK count says 4 (A,B,C,D).
func TestUploadKeyIdempotencyOverlap(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	deviceKeys, oneTimeKeys := alice.MustGenerateOneTimeKeys(t, 4)
	i := 0
	keysABC := map[string]interface{}{}
	keysBCD := map[string]interface{}{}
	for keyID, otk := range oneTimeKeys {
		i++
		if i == 1 {
			keysABC[keyID] = otk
			continue
		}
		if i == 4 {
			keysBCD[keyID] = otk
			continue
		}
		keysABC[keyID] = otk
		keysBCD[keyID] = otk
	}
	t.Logf("OTKs ABC %v", keysABC)
	t.Logf("OTKs BCD %v", keysBCD)
	requests := []client.RequestOpt{
		client.WithJSONBody(t, map[string]interface{}{
			"device_keys": deviceKeys,
		}),
		client.WithJSONBody(t, map[string]interface{}{
			"one_time_keys": keysABC,
		}),
		client.WithJSONBody(t, map[string]interface{}{
			"one_time_keys": keysBCD,
		}),
	}
	for i, reqBody := range requests {
		expectedOTKCount := 0
		if i == 1 {
			expectedOTKCount = 3
		} else if i == 2 {
			expectedOTKCount = 4
		}
		resp := alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "upload"}, reqBody)
		must.MatchResponse(t, resp, match.HTTPResponse{
			StatusCode: http.StatusOK,
			JSON: []match.JSON{
				match.JSONMapEach("one_time_key_counts", func(k, v gjson.Result) error {
					if int(v.Float()) != expectedOTKCount {
						return fmt.Errorf("expected %d one time keys, got %d", expectedOTKCount, int(v.Float()))
					}
					return nil
				}),
			},
		})
	}
}
