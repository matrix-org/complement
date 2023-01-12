//go:build msc3890
// +build msc3890

// This file contains tests for local notification settings as
// defined by MSC3890, which you can read here:
// https://github.com/matrix-org/matrix-doc/pull/3890

package tests

import (
	"testing"
	"time"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
)

func TestDeletingDeviceRemovesDeviceLocalNotificationSettings(t *testing.T) {
	// Create a deployment with a single user
	deployment := Deploy(t, b.BlueprintCleanHS)
	defer deployment.Destroy(t)

	// Create a user which we can log in to multiple times
	alicePassword := "hunter2"
	alice := deployment.RegisterUser(t, "hs1", "alice", alicePassword, false)

	// Log in to another device on this user
	_, secondDeviceAccessToken, secondDeviceID := alice.LoginUser(t, alicePassword)
	secondDevice := &client.CSAPI{
		AccessToken:      secondDeviceAccessToken,
		BaseURL:          deployment.HS["hs1"].BaseURL,
		Client:           client.NewLoggedClient(t, "hs1", nil),
		SyncUntilTimeout: 5 * time.Second,
		Debug:            alice.Debug,
	}

	accountDataType := "org.matrix.msc3890.local_notification_settings." + secondDeviceID
	accountDataContent := map[string]interface{}{"is_silenced": true}

	// Test deleting global account data.
	t.Run("Deleting a user's device should delete any local notification settings entries from their account data", func(t *testing.T) {
		// Retrieve a sync token for this user
		_, nextBatchToken := alice.MustSync(
			t,
			client.SyncReq{},
		)

		// Using the first device, create some local notification settings in the user's account data for the second device.
		alice.SetGlobalAccountData(
			t,
			accountDataType,
			accountDataContent,
		)

		checkAccountDataContent := func(r gjson.Result) bool {
			// Only listen for our test type
			if r.Get("type").Str != accountDataType {
				return false
			}
			content := r.Get("content")

			// Ensure the content of this account data type is as we expect
			return match.JSONDeepEqual([]byte(content.Raw), accountDataContent)
		}

		// Check that the content of the user account data for this type has been set successfully
		alice.MustSyncUntil(
			t,
			client.SyncReq{
				Since: nextBatchToken,
			},
			client.SyncGlobalAccountDataHas(checkAccountDataContent),
		)
		// Also check via the dedicated account data endpoint to ensure the similar check later is not 404'ing for some other reason.
		// Using `MustDoFunc` ensures that the response code is 2xx.
		res := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "user", alice.UserID, "account_data", accountDataType})
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("is_silenced", true),
			},
		})

		// Log out the second device
		secondDevice.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "logout"})

		// Using the first device, check that the local notification setting account data for the deleted device was removed.
		res = alice.DoFunc(t, "GET", []string{"_matrix", "client", "v3", "user", alice.UserID, "account_data", accountDataType})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
			JSON: []match.JSON{
				// A 404 can be generated for missing endpoints as well (which would have an errcode of `M_UNRECOGNIZED`).
				// Ensure we're getting the error we expect.
				match.JSONKeyEqual("errcode", "M_NOT_FOUND"),
			},
		})
	})
}