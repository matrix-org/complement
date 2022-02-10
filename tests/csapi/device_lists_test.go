package csapi_tests

import (
	"fmt"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"

	"github.com/tidwall/gjson"
)

// TestLocalUsersReceiveDeviceListUpdates tests that users on the same
// homeserver receive device list updates from other local users, as
// long as they share a room.
func TestLocalUsersReceiveDeviceListUpdates(t *testing.T) {
	// Create a homeserver with two users that share a room
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	// Get a reference to the already logged-in CS API clients for each user
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	// Deduce alice's device ID
	resp := alice.MustDoFunc(
		t,
		"GET",
		[]string{"_matrix", "client", "v3", "devices"},
	)
	responseBodyBytes := client.ParseJSON(t, resp)
	aliceDeviceID := gjson.GetBytes(responseBodyBytes, "devices.0.device_id").Str

	// Bob performs an initial sync
	_, bobNextBatch := bob.MustSync(t, client.SyncReq{})

	// Alice then updates their device list by renaming their current device
	alice.MustDoFunc(
		t,
		"PUT",
		[]string{"_matrix", "client", "v3", "devices", aliceDeviceID},
		client.WithJSONBody(
			t,
			map[string]interface{}{
				"display_name": "A New Device Name",
			},
		),
	)

	// Check that Bob received a device list update from Alice
	bob.MustSyncUntil(
		t,
		client.SyncReq{
			Since: bobNextBatch,
		}, func(clientUserID string, topLevelSyncJSON gjson.Result) error {
			// Ensure that Bob sees that Alice has updated their device list
			usersWithChangedDeviceListsArray := topLevelSyncJSON.Get("device_lists.changed").Array()
			for _, userID := range usersWithChangedDeviceListsArray {
				if userID.Str == alice.UserID {
					return nil
				}
			}
			return fmt.Errorf("missing device list update for Alice")
		},
	)
}
