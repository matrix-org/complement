//go:build msc3890
// +build msc3890

// This file contains tests for local notification settings as
// defined by MSC3890, which you can read here:
// https://github.com/matrix-org/matrix-doc/pull/3890

package tests

import (
	"testing"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/tidwall/gjson"
)

func TestDeletingDeviceRemovesDeviceLocalNotificationSettings(t *testing.T) {
	// Create a deployment with a single user
	deployment := Deploy(t, b.BlueprintCleanHS)
	defer deployment.Destroy(t)

	// Create a user which we can log in to multiple times
	aliceLocalpart := "alice"
	alicePassword := "hunter2"
	aliceDeviceOne := deployment.RegisterUser(t, "hs1", aliceLocalpart, alicePassword, false)

	// Log in to another device on this user
	aliceDeviceTwo := deployment.LoginUser(t, "hs1", aliceLocalpart, alicePassword)

	accountDataType := "org.matrix.msc3890.local_notification_settings." + aliceDeviceTwo.DeviceID
	accountDataContent := map[string]interface{}{"is_silenced": true}

	// Test deleting global account data.
	t.Run("Deleting a user's device should delete any local notification settings entries from their account data", func(t *testing.T) {
		// Retrieve a sync token for this user
		_, nextBatchToken := aliceDeviceOne.MustSync(
			t,
			client.SyncReq{},
		)

		// Using the first device, create some local notification settings in the user's account data for the second device.
		aliceDeviceOne.SetGlobalAccountData(
			t,
			accountDataType,
			accountDataContent,
		)

		checkAccountDataContent := func(r gjson.Result) bool {
			// Only listen for our test type
			if r.Get("type").Str != accountDataType {
				return false
			}
			return match.JSONKeyEqual("content", accountDataContent)(r) == nil
		}

		// Check that the content of the user account data for this type has been set successfully
		aliceDeviceOne.MustSyncUntil(
			t,
			client.SyncReq{
				Since: nextBatchToken,
			},
			client.SyncGlobalAccountDataHas(checkAccountDataContent),
		)
		// Also check via the dedicated account data endpoint to ensure the similar check later is not 404'ing for some other reason.
		// Using `MustDo` ensures that the response code is 2xx.
		res := aliceDeviceOne.MustDo(t, "GET", []string{"_matrix", "client", "v3", "user", aliceDeviceOne.UserID, "account_data", accountDataType})
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("is_silenced", true),
			},
		})

		// Log out the second device
		aliceDeviceTwo.MustDo(t, "POST", []string{"_matrix", "client", "v3", "logout"})

		// Using the first device, check that the local notification setting account data for the deleted device was removed.
		res = aliceDeviceOne.Do(t, "GET", []string{"_matrix", "client", "v3", "user", aliceDeviceOne.UserID, "account_data", accountDataType})
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
