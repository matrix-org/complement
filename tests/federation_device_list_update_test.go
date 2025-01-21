package tests

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
	"github.com/matrix-org/gomatrixserverlib"
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
			alice2.MustUploadKeys(t, deviceKeys, oneTimeKeys)

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

// Regression test for https://github.com/matrix-org/synapse/issues/11374
// In this test, we'll make a room on the Complement server and get a user on the
// HS to join it. We will ensure that we get sent a device list update EDU. We should
// be sent this EDU according to the specification:
//
//	> Servers must send m.device_list_update EDUs to all the servers who share a room with a given local user,
//	> and must be sent whenever that user’s device list changes (i.e. for new or deleted devices, when that
//	> user joins a room which contains servers which are not already receiving updates for that user’s device
//	> list, or changes in device information such as the device’s human-readable name).
func TestDeviceListsUpdateOverFederationOnRoomJoin(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite, runtime.Synapse) // https://github.com/element-hq/synapse/pull/16875#issuecomment-1923446390
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
		Password:        "this is alices password",
	})

	waiter := helpers.NewWaiter()
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(nil,
			func(e gomatrixserverlib.EDU) {
				t.Logf("got edu: %+v", e)
				if e.Type == "m.device_list_update" {
					content := gjson.ParseBytes(e.Content)
					if content.Get("user_id").Str == alice.UserID && content.Get("device_id").Str == alice.DeviceID {
						waiter.Finish()
					}
				}
			},
		),
	)
	srv.UnexpectedRequestsAreErrors = false // we expect to be pushed events
	cancel := srv.Listen()
	defer cancel()

	bob := srv.UserID("complement_bob")
	roomVer := gomatrixserverlib.RoomVersion("10")
	initalEvents := federation.InitialRoomEvents(roomVer, bob)
	room := srv.MustMakeRoom(t, roomVer, initalEvents)

	alice.MustJoinRoom(t, room.RoomID, []string{srv.ServerName()})
	alice.SendEventSynced(t, room.RoomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.body",
			"body":    "Test",
		},
	})
	waiter.Wait(t, 10*time.Second)
}

// Related to the previous test TestDeviceListsUpdateOverFederationOnRoomJoin
// In this test, we make 2 homeservers and join the same room. We ensure that the
// joinee sees the joiner's user ID in `device_lists.changed` of the /sync response.
// If this happens, the test then hits `/keys/query` for that user ID  to ensure
// that the joinee sees the joiner's device ID.
func TestUserAppearsInChangedDeviceListOnJoinOverFederation(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)
	joiner := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "joiner",
	})
	joinee := deployment.Register(t, "hs2", helpers.RegistrationOpts{
		LocalpartSuffix: "joinee",
	})

	// the joiner needs device keys so /keys/query works..
	joinerDeviceKeys, joinerOTKs := joiner.MustGenerateOneTimeKeys(t, 5)
	joiner.MustUploadKeys(t, joinerDeviceKeys, joinerOTKs)

	// they must share a room to get device list updates
	roomID := joinee.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
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

	_, since := joinee.MustSync(t, client.SyncReq{})

	// the joiner now joins the room over federation
	joiner.MustJoinRoom(t, roomID, []string{"hs2"})

	// we must see the joiner's user ID in device_lists.changed
	since = joinee.MustSyncUntil(t, client.SyncReq{
		Since: since,
	}, func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		changed := topLevelSyncJSON.Get("device_lists.changed").Array()
		for _, userID := range changed {
			if userID.Str == joiner.UserID {
				return nil
			}
		}
		return fmt.Errorf("did not see joiner's user ID in device_lists.changed: %v", topLevelSyncJSON.Get("device_lists").Raw)
	})

	// if we got here, we saw the joiner's user ID, so hit /keys/query to get the device ID
	res := joinee.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "query"}, client.WithJSONBody(t, map[string]any{
		"device_keys": map[string]any{
			joiner.UserID: []string{}, // all device IDs
		},
	}))
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyPresent(fmt.Sprintf(
				"device_keys.%s.%s", joiner.UserID, joiner.DeviceID,
			)),
		},
	})
}
