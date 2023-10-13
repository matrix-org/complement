// This file contains tests for local notification settings as
// defined by MSC3890, which you can read here:
// https://github.com/matrix-org/matrix-doc/pull/3890

package tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/tidwall/gjson"
)

func TestDeletingDeviceRemovesDeviceLocalNotificationSettings(t *testing.T) {
	// Create a deployment with a single user
	deployment := complement.Deploy(t, b.BlueprintCleanHS)
	defer deployment.Destroy(t)

	t.Log("Alice registers on device 1 and logs in to device 2.")
	aliceLocalpart := "alice"
	alicePassword := "hunter2"
	aliceDeviceOne := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: aliceLocalpart,
		Password:        alicePassword,
	})
	aliceDeviceTwo := deployment.Login(t, "hs1", aliceDeviceOne, helpers.LoginOpts{
		Password: alicePassword,
	})

	accountDataType := "org.matrix.msc3890.local_notification_settings." + aliceDeviceTwo.DeviceID
	accountDataContent := map[string]interface{}{"is_silenced": true}

	// Test deleting global account data.
	t.Run("Deleting a user's device should delete any local notification settings entries from their account data", func(t *testing.T) {
		// Retrieve a sync token for this user
		_, nextBatchToken := aliceDeviceOne.MustSync(
			t,
			client.SyncReq{},
		)

		t.Log("Using her first device, Alice creates some local notification settings in her account data for the second device.")
		aliceDeviceOne.MustSetGlobalAccountData(
			t, accountDataType, accountDataContent,
		)

		checkAccountDataContent := func(r gjson.Result) bool {
			// Only listen for our test type
			if r.Get("type").Str != accountDataType {
				return false
			}
			return match.JSONKeyEqual("content", accountDataContent)(r) == nil
		}

		t.Log("Alice syncs on device 1 until she sees the account data she just wrote.")
		aliceDeviceOne.MustSyncUntil(
			t,
			client.SyncReq{
				Since: nextBatchToken,
			},
			client.SyncGlobalAccountDataHas(checkAccountDataContent),
		)

		t.Log("Alice also checks for the account data she wrote on the dedicated account data endpoint.")
		res := aliceDeviceOne.MustGetGlobalAccountData(t, accountDataType)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("is_silenced", true),
			},
		})

		t.Log("Alice logs out her second device.")
		aliceDeviceTwo.MustDo(t, "POST", []string{"_matrix", "client", "v3", "logout"})

		t.Log("Alice re-fetches the global account data. The response should now have status 404.")
		res = aliceDeviceOne.GetGlobalAccountData(t, accountDataType)
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
