package csapi_tests

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

func TestKeyChangesLocal(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	password := "$uperSecretPassword"
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "bob",
		Password:        password,
	})
	unauthedClient := deployment.UnauthenticatedClient(t, "hs1")

	t.Run("New login should create a device_lists.changed entry", func(t *testing.T) {
		bobDeviceKeys, bobOTKs := bob.MustGenerateOneTimeKeys(t, 1)
		bob.MustUploadKeys(t, bobDeviceKeys, bobOTKs)

		roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
		bob.MustJoinRoom(t, roomID, []string{})
		nextBatch1 := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"identifier": map[string]interface{}{
				"type": "m.id.user",
				"user": bob.UserID,
			},
			"type":     "m.login.password",
			"password": password,
		})
		// Create a new device by logging in
		res := unauthedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "login"}, reqBody)
		loginResp := must.ParseJSON(t, res.Body)
		unauthedClient.AccessToken = must.GetJSONFieldStr(t, loginResp, "access_token")
		unauthedClient.DeviceID = must.GetJSONFieldStr(t, loginResp, "device_id")
		unauthedClient.UserID = must.GetJSONFieldStr(t, loginResp, "user_id")
		unauthedKeys, unauthedOTKs := unauthedClient.MustGenerateOneTimeKeys(t, 1)
		unauthedClient.MustUploadKeys(t, unauthedKeys, unauthedOTKs)

		// Alice should now see a device list changed entry for Bob
		nextBatch := alice.MustSyncUntil(t, client.SyncReq{Since: nextBatch1}, func(userID string, syncResp gjson.Result) error {
			deviceListsChanged := syncResp.Get("device_lists.changed")
			if !deviceListsChanged.IsArray() {
				return fmt.Errorf("no device_lists.changed entry found: %+v", syncResp.Raw)
			}
			for _, userID := range deviceListsChanged.Array() {
				if userID.String() == bob.UserID {
					return nil
				}
			}
			return fmt.Errorf("no device_lists.changed entry found for %s", bob.UserID)
		})
		// Verify on /keys/changes that Bob has changes
		queryParams := url.Values{}
		queryParams.Set("from", nextBatch1)
		queryParams.Set("to", nextBatch)
		resp := alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "keys", "changes"}, client.WithQueries(queryParams))
		must.MatchResponse(t, resp, match.HTTPResponse{
			StatusCode: http.StatusOK,
			JSON: []match.JSON{
				match.JSONKeyEqual("changed.0", bob.UserID), // there should only be one change, so access it directly
			},
		})

		// Get Bobs keys, there should be two
		queryKeys := client.WithJSONBody(t, map[string]interface{}{
			"device_keys": map[string][]string{
				bob.UserID: {},
			},
		})
		resp = alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "query"}, queryKeys)
		keyCount := 0
		must.MatchResponse(t, resp, match.HTTPResponse{
			StatusCode: http.StatusOK,
			JSON: []match.JSON{
				match.JSONMapEach("device_keys."+bob.UserID, func(k, v gjson.Result) error {
					keyCount++
					return nil
				}),
			},
		})
		wantKeyCount := 2
		if keyCount != wantKeyCount {
			t.Fatalf("unexpected key count: got %d, want %d", keyCount, wantKeyCount)
		}
	})
}
