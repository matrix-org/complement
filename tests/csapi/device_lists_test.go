package csapi_tests

import (
	"fmt"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/matrix-org/complement/runtime"
	"maunium.net/go/mautrix/crypto/olm"

	"github.com/tidwall/gjson"
)

// TestDeviceListUpdates tests various flows and checks that:
//  1. `/sync`'s `device_lists.changed/left` contain the correct user IDs.
//  2. `/keys/query` returns the correct information after device list updates.
func TestDeviceListUpdates(t *testing.T) {
	localpartIndex := 0
	// generateLocalpart generates a unique localpart based on the given name.
	generateLocalpart := func(localpart string) string {
		localpartIndex++
		return fmt.Sprintf("%s%d", localpart, localpartIndex)
	}

	// uploadNewKeys uploads a new set of keys for a given client.
	// Returns a check function that can be passed to mustQueryKeys.
	uploadNewKeys := func(t *testing.T, user *client.CSAPI) []match.JSON {
		t.Helper()

		account := olm.NewAccount()
		ed25519Key, curve25519Key := account.IdentityKeys()

		ed25519KeyID := fmt.Sprintf("ed25519:%s", user.DeviceID)
		curve25519KeyID := fmt.Sprintf("curve25519:%s", user.DeviceID)

		user.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "keys", "upload"},
			client.WithJSONBody(t, map[string]interface{}{
				"device_keys": map[string]interface{}{
					"user_id":    user.UserID,
					"device_id":  user.DeviceID,
					"algorithms": []interface{}{"m.olm.v1.curve25519-aes-sha2", "m.megolm.v1.aes-sha2"},
					"keys": map[string]interface{}{
						ed25519KeyID:    ed25519Key.String(),
						curve25519KeyID: curve25519Key.String(),
					},
				},
			}),
		)

		algorithmsPath := fmt.Sprintf("device_keys.%s.%s.algorithms", user.UserID, user.DeviceID)
		ed25519Path := fmt.Sprintf("device_keys.%s.%s.keys.%s", user.UserID, user.DeviceID, ed25519KeyID)
		curve25519Path := fmt.Sprintf("device_keys.%s.%s.keys.%s", user.UserID, user.DeviceID, curve25519KeyID)
		return []match.JSON{
			match.JSONKeyEqual(algorithmsPath, []interface{}{"m.olm.v1.curve25519-aes-sha2", "m.megolm.v1.aes-sha2"}),
			match.JSONKeyEqual(ed25519Path, ed25519Key.String()),
			match.JSONKeyEqual(curve25519Path, curve25519Key.String()),
		}
	}

	// mustQueryKeys checks that /keys/query returns the correct device keys.
	// Accepts a check function produced by a prior call to uploadNewKeys.
	mustQueryKeys := func(t *testing.T, user *client.CSAPI, userID string, check []match.JSON) {
		t.Helper()

		res := user.DoFunc(t, "POST", []string{"_matrix", "client", "v3", "keys", "query"},
			client.WithJSONBody(t, map[string]interface{}{
				"device_keys": map[string]interface{}{
					userID: []string{},
				},
			}),
		)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
			JSON:       check,
		})
	}

	// syncDeviceListsHas checks that `device_lists.changed` or `device_lists.left` contains a given
	// user ID.
	syncDeviceListsHas := func(section string, expectedUserID string) client.SyncCheckOpt {
		jsonPath := fmt.Sprintf("device_lists.%s", section)
		return func(clientUserID string, topLevelSyncJSON gjson.Result) error {
			usersWithChangedDeviceListsArray := topLevelSyncJSON.Get(jsonPath).Array()
			for _, userID := range usersWithChangedDeviceListsArray {
				if userID.Str == expectedUserID {
					return nil
				}
			}
			return fmt.Errorf(
				"syncDeviceListsHas: %s not found in %s",
				expectedUserID,
				jsonPath,
			)
		}
	}

	// In all of these test scenarios, there are two users: Alice and Bob.
	// We only care about what Alice sees.

	// testOtherUserJoin tests another user joining a room Alice is already in.
	testOtherUserJoin := func(t *testing.T, deployment *docker.Deployment, hsName string, otherHSName string) {
		alice := deployment.RegisterUser(t, hsName, generateLocalpart("alice"), "password", false)
		bob := deployment.RegisterUser(t, otherHSName, generateLocalpart("bob"), "password", false)
		checkBobKeys := uploadNewKeys(t, bob)

		roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})

		// Alice performs an initial sync
		_, aliceNextBatch := alice.MustSync(t, client.SyncReq{})

		// Bob joins the room
		bob.JoinRoom(t, roomID, []string{hsName})
		bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

		// Check that Alice receives a device list update from Bob
		alice.MustSyncUntil(
			t,
			client.SyncReq{Since: aliceNextBatch},
			syncDeviceListsHas("changed", bob.UserID),
		)
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)
		// Some homeservers (Synapse) may emit another `changed` update after querying keys.
		_, aliceNextBatch = alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

		// Both homeservers think Bob has joined now
		// Bob then updates their device list
		checkBobKeys = uploadNewKeys(t, bob)

		// Check that Alice receives a device list update from Bob
		alice.MustSyncUntil(
			t,
			client.SyncReq{Since: aliceNextBatch},
			syncDeviceListsHas("changed", bob.UserID),
		)
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)
	}

	// testJoin tests Alice joining a room another user is already in.
	testJoin := func(
		t *testing.T, deployment *docker.Deployment, hsName string, otherHSName string,
	) {
		alice := deployment.RegisterUser(t, hsName, generateLocalpart("alice"), "password", false)
		bob := deployment.RegisterUser(t, otherHSName, generateLocalpart("bob"), "password", false)
		checkBobKeys := uploadNewKeys(t, bob)

		roomID := bob.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})

		// Alice performs an initial sync
		_, aliceNextBatch := alice.MustSync(t, client.SyncReq{})

		// Alice joins the room
		alice.JoinRoom(t, roomID, []string{otherHSName})
		bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

		// Check that Alice receives a device list update from Bob
		alice.MustSyncUntil(
			t,
			client.SyncReq{Since: aliceNextBatch},
			syncDeviceListsHas("changed", bob.UserID),
		)
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)
		// Some homeservers (Synapse) may emit another `changed` update after querying keys.
		_, aliceNextBatch = alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

		// Both homeservers think Alice has joined now
		// Bob then updates their device list
		checkBobKeys = uploadNewKeys(t, bob)

		// Check that Alice receives a device list update from Bob
		alice.MustSyncUntil(
			t,
			client.SyncReq{Since: aliceNextBatch},
			syncDeviceListsHas("changed", bob.UserID),
		)
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)
	}

	// testOtherUserLeave tests another user leaving a room Alice is in.
	testOtherUserLeave := func(t *testing.T, deployment *docker.Deployment, hsName string, otherHSName string) {
		alice := deployment.RegisterUser(t, hsName, generateLocalpart("alice"), "password", false)
		bob := deployment.RegisterUser(t, otherHSName, generateLocalpart("bob"), "password", false)
		checkBobKeys := uploadNewKeys(t, bob)

		roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})

		// Bob joins the room
		bob.JoinRoom(t, roomID, []string{hsName})
		bobNextBatch := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

		// Alice performs an initial sync
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)
		// Some homeservers (Synapse) may emit another `changed` update after querying keys.
		_, aliceNextBatch := alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

		// Bob leaves the room
		bob.LeaveRoom(t, roomID)
		bob.MustSyncUntil(t, client.SyncReq{Since: bobNextBatch}, client.SyncLeftFrom(bob.UserID, roomID))

		// Check that Alice is notified that she will no longer receive updates about Bob's devices
		aliceNextBatch = alice.MustSyncUntil(
			t,
			client.SyncReq{Since: aliceNextBatch},
			syncDeviceListsHas("left", bob.UserID),
		)

		// Both homeservers think Bob has left now
		// Bob then updates their device list
		// Alice's homeserver is not expected to get the device list update and must not return a
		// cached device list for Bob.
		checkBobKeys = uploadNewKeys(t, bob)
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)

		// Check that Alice is not notified about Bob's device update
		syncResult, _ := alice.MustSync(t, client.SyncReq{Since: aliceNextBatch})
		if syncDeviceListsHas("changed", bob.UserID)(alice.UserID, syncResult) == nil {
			t.Fatalf("Alice was unexpectedly notified about Bob's device update even though they share no rooms")
		}
	}

	// testLeave tests Alice leaving a room another user is in.
	testLeave := func(t *testing.T, deployment *docker.Deployment, hsName string, otherHSName string) {
		alice := deployment.RegisterUser(t, hsName, generateLocalpart("alice"), "password", false)
		bob := deployment.RegisterUser(t, otherHSName, generateLocalpart("bob"), "password", false)
		checkBobKeys := uploadNewKeys(t, bob)

		roomID := bob.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})

		// Alice joins the room
		alice.JoinRoom(t, roomID, []string{otherHSName})
		bobNextBatch := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

		// Alice performs an initial sync
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)
		// Some homeservers (Synapse) may emit another `changed` update after querying keys.
		_, aliceNextBatch := alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

		// Alice leaves the room
		alice.LeaveRoom(t, roomID)
		bob.MustSyncUntil(t, client.SyncReq{Since: bobNextBatch}, client.SyncLeftFrom(alice.UserID, roomID))

		// Check that Alice is notified that she will no longer receive updates about Bob's devices
		aliceNextBatch = alice.MustSyncUntil(
			t,
			client.SyncReq{Since: aliceNextBatch},
			syncDeviceListsHas("left", bob.UserID),
		)

		// Both homeservers think Alice has left now
		// Bob then updates their device list
		// Alice's homeserver is not expected to get the device list update and must not return a
		// cached device list for Bob.
		checkBobKeys = uploadNewKeys(t, bob)
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)

		// Check that Alice is not notified about Bob's device update
		syncResult, _ := alice.MustSync(t, client.SyncReq{Since: aliceNextBatch})
		if syncDeviceListsHas("changed", bob.UserID)(alice.UserID, syncResult) == nil {
			t.Fatalf("Alice was unexpectedly notified about Bob's device update even though they share no rooms")
		}
	}

	// testOtherUserRejoin tests another user leaving and rejoining a room Alice is in.
	testOtherUserRejoin := func(t *testing.T, deployment *docker.Deployment, hsName string, otherHSName string) {
		alice := deployment.RegisterUser(t, hsName, generateLocalpart("alice"), "password", false)
		bob := deployment.RegisterUser(t, otherHSName, generateLocalpart("bob"), "password", false)
		checkBobKeys := uploadNewKeys(t, bob)

		roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})

		// Bob joins the room
		bob.JoinRoom(t, roomID, []string{hsName})
		bobNextBatch := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

		// Alice performs an initial sync
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)
		// Some homeservers (Synapse) may emit another `changed` update after querying keys.
		_, aliceNextBatch := alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

		// Both homeservers think Bob has joined now
		// Bob leaves the room
		bob.LeaveRoom(t, roomID)
		bobNextBatch = bob.MustSyncUntil(t, client.SyncReq{Since: bobNextBatch}, client.SyncLeftFrom(bob.UserID, roomID))

		// Check that Alice is notified that she will no longer receive updates about Bob's devices
		alice.MustSyncUntil(
			t,
			client.SyncReq{Since: aliceNextBatch},
			syncDeviceListsHas("left", bob.UserID),
		)

		// Both homeservers think Bob has left now
		// Bob then updates their device list before rejoining the room
		// Alice's homeserver is not expected to get the device list update.
		checkBobKeys = uploadNewKeys(t, bob)

		// Bob rejoins the room
		bob.JoinRoom(t, roomID, []string{hsName})
		bob.MustSyncUntil(t, client.SyncReq{Since: bobNextBatch}, client.SyncJoinedTo(bob.UserID, roomID))

		// Check that Alice is notified that Bob's devices have a change
		// Alice's homeserver must not return a cached device list for Bob.
		alice.MustSyncUntil(
			t,
			client.SyncReq{Since: aliceNextBatch},
			syncDeviceListsHas("changed", bob.UserID),
		)
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)
	}

	// Create two homeservers
	// The users and rooms in the blueprint won't be used.
	// Each test creates their own Alice and Bob users.
	deployment := Deploy(t, b.BlueprintFederationOneToOneRoom)
	defer deployment.Destroy(t)

	t.Run("when local user joins a room", func(t *testing.T) { testOtherUserJoin(t, deployment, "hs1", "hs1") })
	t.Run("when remote user joins a room", func(t *testing.T) { testOtherUserJoin(t, deployment, "hs1", "hs2") })
	t.Run("when joining a room with a local user", func(t *testing.T) { testJoin(t, deployment, "hs1", "hs1") })
	t.Run("when joining a room with a remote user", func(t *testing.T) { testJoin(t, deployment, "hs1", "hs2") })
	t.Run("when local user leaves a room", func(t *testing.T) { testOtherUserLeave(t, deployment, "hs1", "hs1") })
	t.Run("when remote user leaves a room", func(t *testing.T) { testOtherUserLeave(t, deployment, "hs1", "hs2") })
	t.Run("when leaving a room with a local user", func(t *testing.T) { testLeave(t, deployment, "hs1", "hs1") })
	t.Run("when leaving a room with a remote user", func(t *testing.T) {
		runtime.SkipIf(t, runtime.Synapse) // FIXME: https://github.com/matrix-org/synapse/issues/13650
		testLeave(t, deployment, "hs1", "hs2")
	})
	t.Run("when local user rejoins a room", func(t *testing.T) { testOtherUserRejoin(t, deployment, "hs1", "hs1") })
	t.Run("when remote user rejoins a room", func(t *testing.T) { testOtherUserRejoin(t, deployment, "hs1", "hs2") })
}
