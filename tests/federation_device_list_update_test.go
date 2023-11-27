package tests

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/tidwall/gjson"
)

// Test that device list updates can go from one homeserver to another.
func TestDeviceListsUpdateOverFederation(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	syncHasDeviceListChange := func(changed []string, left []string) client.SyncCheckOpt {
		sort.Strings(changed)
		sort.Strings(left)
		return func(clientUserID string, topLevelSyncJSON gjson.Result) error {
			dl := topLevelSyncJSON.Get("device_lists")
			changedJSON := dl.Get("changed").Array()
			leftJSON := dl.Get("left").Array()
			gotChanged := make([]string, len(changedJSON))
			gotLeft := make([]string, len(leftJSON))
			for i := range gotChanged {
				gotChanged[i] = changedJSON[i].Str
			}
			for i := range gotLeft {
				gotLeft[i] = leftJSON[i].Str
			}
			sort.Strings(gotChanged)
			sort.Strings(gotLeft)
			changedMatch := reflect.DeepEqual(changed, gotChanged)
			leftMatch := reflect.DeepEqual(left, gotLeft)
			if changedMatch && leftMatch {
				return nil
			}
			return fmt.Errorf("syncHasDeviceListChange: got changed %v want %v, got left %v want %v", gotChanged, changed, gotLeft, left)
		}
	}

	testCases := []struct {
		name            string
		makeUnreachable func(t *testing.T)
		makeReachable   func(t *testing.T)
	}{
		{
			name:            "good connectivity",
			makeUnreachable: func(t *testing.T) {},
			makeReachable:   func(t *testing.T) {},
		},
		{
			// cut networking but keep in-memory state
			name: "interrupted connectivity",
			makeUnreachable: func(t *testing.T) {
				deployment.StopServer(t, "hs2")
			},
			makeReachable: func(t *testing.T) {
				deployment.StartServer(t, "hs2")
			},
		},
		{
			// interesting because this nukes memory
			name: "stopped server",
			makeUnreachable: func(t *testing.T) {
				deployment.StopServer(t, "hs2")
			},
			makeReachable: func(t *testing.T) {
				// kick over the sending server first to see if the server
				// remembers to resend on startup
				deployment.StopServer(t, "hs1")
				deployment.StartServer(t, "hs1")
				// now make the receiving server reachable.
				deployment.StartServer(t, "hs2")
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
				LocalpartSuffix: "alice",
				Password:        "this is alices password",
			})
			bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{
				LocalpartSuffix: "bob",
			})
			// they must share a room to get device list updates
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
				"invite": []string{bob.UserID},
				// we strictly don't need to make this an encrypted room, but there are some
				// issues which state that we should only share device list updates for
				// users in shared encrypted rooms, so let's ensure we do that.
				// See https://github.com/matrix-org/synapse/issues/7524
				"initial_state": []map[string]interface{}{
					{
						"type":      "m.room.encryption",
						"state_key": "",
						"content": map[string]interface{}{
							"algorithm": "m.megolm.v1.aes-sha2",
						},
					},
				},
			})
			// it might take a while for retries, so keep on syncing!
			bob.SyncUntilTimeout = 50 * time.Second
			_, aliceSince := alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})
			bobSince := bob.MustSyncUntil(t, client.SyncReq{TimeoutMillis: "0"}, client.SyncInvitedTo(bob.UserID, roomID))

			bob.MustJoinRoom(t, roomID, []string{"hs1"})

			// both alice and bob should see device list updates for each other
			aliceSince = alice.MustSyncUntil(
				t, client.SyncReq{TimeoutMillis: "1000", Since: aliceSince},
				syncHasDeviceListChange([]string{bob.UserID}, []string{}),
			)
			bobSince = bob.MustSyncUntil(
				t, client.SyncReq{TimeoutMillis: "1000", Since: bobSince},
				// bob is in this list because... his other devices may need to know.
				syncHasDeviceListChange([]string{alice.UserID, bob.UserID}, []string{}),
			)

			// now federation is going to be interrupted...
			tc.makeUnreachable(t)

			// ..and alice logs in on a new device!
			alice2 := deployment.Login(t, "hs1", alice, helpers.LoginOpts{
				DeviceID: "NEW_DEVICE",
				Password: "this is alices password",
			})
			deviceKeys, oneTimeKeys := alice2.MustGenerateOneTimeKeys(t, 1)
			alice2.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "upload"}, client.WithJSONBody(t, map[string]interface{}{
				"device_keys":   deviceKeys,
				"one_time_keys": oneTimeKeys,
			}))

			// now federation comes back online
			tc.makeReachable(t)

			// ensure alice sees her new device login
			aliceSince = alice.MustSyncUntil(
				t, client.SyncReq{TimeoutMillis: "1000", Since: aliceSince},
				syncHasDeviceListChange([]string{alice.UserID}, []string{}),
			)

			// ensure bob sees the device list change
			bobSince = bob.MustSyncUntil(
				t, client.SyncReq{TimeoutMillis: "1000", Since: bobSince},
				syncHasDeviceListChange([]string{alice.UserID}, []string{}),
			)
		})
	}
}
