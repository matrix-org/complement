package csapi_tests

import (
	"fmt"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

type backupKey struct {
	isVerified        bool
	firstMessageIndex float64
	forwardedCount    float64
}

// This test checks that the rules for replacing room keys are implemented correctly.
// Specifically:
//
//	if the keys have different values for is_verified, then it will keep the key that has is_verified set to true;
//	if they have the same values for is_verified, then it will keep the key with a lower first_message_index;
//	and finally, is is_verified and first_message_index are equal, then it will keep the key with a lower forwarded_count.
func TestE2EKeyBackupReplaceRoomKeyRules(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	userID := "@alice:hs1"
	roomID := "!foo:hs1"
	alice := deployment.Client(t, "hs1", userID)

	// make a new key backup
	res := alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "room_keys", "version"}, client.WithJSONBody(t, map[string]interface{}{
		"algorithm": "m.megolm_backup.v1",
		"auth_data": map[string]interface{}{
			"foo": "bar",
		},
	}))
	defer res.Body.Close()
	body := must.ParseJSON(t, res.Body)
	backupVersion := gjson.GetBytes(body, "version").Str
	if backupVersion == "" {
		t.Fatalf("failed to get 'version' key from response: %s", string(body))
	}

	testCases := []struct {
		sessionID           string      // change this for each test case to namespace tests correctly
		input               backupKey   // the key to compare against
		keysThatDontReplace []backupKey // the keys which won't update the input key
	}{
		{
			sessionID: "a",
			input: backupKey{
				isVerified:        false,
				firstMessageIndex: 10,
				forwardedCount:    5,
			},
			keysThatDontReplace: []backupKey{
				{
					isVerified:        false,
					firstMessageIndex: 11, // higher first message index
					forwardedCount:    5,
				},
				{
					isVerified:        false,
					firstMessageIndex: 10,
					forwardedCount:    6, // higher forwarded count
				},
				{
					isVerified:        false,
					firstMessageIndex: 11, // higher first message index
					forwardedCount:    6,  // higher forwarded count
				},
			},
		},
		{
			sessionID: "b",
			input: backupKey{
				isVerified:        true,
				firstMessageIndex: 10,
				forwardedCount:    5,
			},
			keysThatDontReplace: []backupKey{
				{
					isVerified:        false, // is verified is false
					firstMessageIndex: 11,    // higher first message index
					forwardedCount:    5,
				},
				{
					isVerified:        false, // is verified is false
					firstMessageIndex: 10,
					forwardedCount:    6, // higher forwarded count
				},
				{
					isVerified:        false, // is verified is false
					firstMessageIndex: 11,    // higher first message index
					forwardedCount:    6,     // higher forwarded count
				},
				{
					isVerified:        true,
					firstMessageIndex: 11, // higher first message index
					forwardedCount:    5,
				},
				{
					isVerified:        true,
					firstMessageIndex: 10,
					forwardedCount:    6, // higher forwarded count
				},
				{
					isVerified:        true,
					firstMessageIndex: 11, // higher first message index
					forwardedCount:    6,  // higher forwarded count
				},
			},
		},
	}

	t.Run("parallel", func(t *testing.T) {
		for i := range testCases {
			tc := testCases[i]
			t.Run(fmt.Sprintf("%+v", tc.input), func(t *testing.T) {
				t.Parallel()
				// insert the key that will be tested against
				alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "room_keys", "keys", roomID, tc.sessionID},
					client.WithQueries(map[string][]string{"version": {backupVersion}}), client.WithJSONBody(t, map[string]interface{}{
						"first_message_index": tc.input.firstMessageIndex,
						"forwarded_count":     tc.input.forwardedCount,
						"is_verified":         tc.input.isVerified,
						"session_data":        map[string]interface{}{"a": "b"},
					}),
				)
				// now check that each key in keysThatDontReplace do not replace this key
				for _, testKey := range tc.keysThatDontReplace {
					alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "room_keys", "keys", roomID, tc.sessionID},
						client.WithQueries(map[string][]string{"version": {backupVersion}}), client.WithJSONBody(t, map[string]interface{}{
							"first_message_index": testKey.firstMessageIndex,
							"forwarded_count":     testKey.forwardedCount,
							"is_verified":         testKey.isVerified,
							"session_data":        map[string]interface{}{"a": "b"},
						}),
					)
					checkResp := alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "room_keys", "keys", roomID, tc.sessionID},
						client.WithQueries(map[string][]string{"version": {backupVersion}}),
					)
					must.MatchResponse(t, checkResp, match.HTTPResponse{
						StatusCode: 200,
						JSON: []match.JSON{
							match.JSONKeyEqual("first_message_index", tc.input.firstMessageIndex),
							match.JSONKeyEqual("forwarded_count", tc.input.forwardedCount),
							match.JSONKeyEqual("is_verified", tc.input.isVerified),
						},
					})
				}
			})
		}
	})
}
