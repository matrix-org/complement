package csapi_tests

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
	"maunium.net/go/mautrix/crypto/olm"

	"github.com/tidwall/gjson"
)

// TestDeviceListUpdates tests various flows and checks that:
//  1. `/sync`'s `device_lists.changed/left` contain the correct user IDs.
//  2. `/keys/query` returns the correct information after device list updates.
func TestDeviceListUpdates(t *testing.T) {
	var localpartIndex int64 = 0
	// generateLocalpart generates a unique localpart based on the given name.
	generateLocalpart := func(localpart string) string {
		index := atomic.AddInt64(&localpartIndex, 1)
		return fmt.Sprintf("%s%d", localpart, index)
	}

	// uploadNewKeys uploads a new set of keys for a given client.
	// Returns a check function that can be passed to mustQueryKeys.
	uploadNewKeys := func(t *testing.T, user *client.CSAPI) []match.JSON {
		t.Helper()

		account := olm.NewAccount()
		ed25519Key, curve25519Key := account.IdentityKeys()

		ed25519KeyID := fmt.Sprintf("ed25519:%s", user.DeviceID)
		curve25519KeyID := fmt.Sprintf("curve25519:%s", user.DeviceID)

		user.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "upload"},
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

		res := user.Do(t, "POST", []string{"_matrix", "client", "v3", "keys", "query"},
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

	// makeBarrier returns a function which tries to act as a barrier for `device_lists.changed`
	// updates.
	//
	// When a remote user has a device list update, an entry is expected to appear in
	// `device_lists.changed` in the `/sync` response. The local homeserver may then query the
	// remote homeserver for the update. Some homeservers (Synapse) may emit extra
	// `device_lists.changed` updates in `/sync` responses after querying keys.
	//
	// The barrier tries to ensure that `device_lists.changed` entries resulting from device list
	// updates and queries before the barrier do not appear in `/sync` responses after the barrier.
	makeBarrier := func(
		t *testing.T,
		deployment complement.Deployment,
		observingUser *client.CSAPI,
		otherHSName string,
	) func(t *testing.T, nextBatch string) string {
		t.Helper()

		barry := deployment.Register(t, otherHSName, helpers.RegistrationOpts{
			Localpart: generateLocalpart("barry"),
			Password:  "password",
		})

		// The observing user must share a room with the dummy barrier user.
		roomID := barry.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
		observingUser.MustJoinRoom(t, roomID, []string{otherHSName})
		observingUser.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(observingUser.UserID, roomID))

		return func(t *testing.T, nextBatch string) string {
			// Publish a device list update from the barrier user and wait until the observing user
			// sees it.
			t.Logf("Sending and waiting for dummy device list update...")
			uploadNewKeys(t, barry)
			return observingUser.MustSyncUntil(
				t,
				client.SyncReq{Since: nextBatch},
				syncDeviceListsHas("changed", barry.UserID),
			)
		}
	}

	// In all of these test scenarios, there are two users: Alice and Bob.
	// We only care about what Alice sees.

	// testOtherUserJoin tests another user joining a room Alice is already in.
	testOtherUserJoin := func(t *testing.T, deployment complement.Deployment, hsName string, otherHSName string) {
		alice := deployment.Register(t, hsName, helpers.RegistrationOpts{
			Localpart: generateLocalpart("alice"),
			Password:  "password",
		})
		bob := deployment.Register(t, otherHSName, helpers.RegistrationOpts{
			Localpart: generateLocalpart("bob"),
			Password:  "password",
		})
		barrier := makeBarrier(t, deployment, alice, otherHSName)
		checkBobKeys := uploadNewKeys(t, bob)

		roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
		t.Logf("%s created test room %s.", alice.UserID, roomID)

		// Alice performs an initial sync
		_, aliceNextBatch := alice.MustSync(t, client.SyncReq{})

		// Bob joins the room
		t.Logf("%s joins the test room.", bob.UserID)
		bob.MustJoinRoom(t, roomID, []string{hsName})
		bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

		// Check that Alice receives a device list update from Bob
		t.Logf("%s expects a device list change for %s...", alice.UserID, bob.UserID)
		aliceNextBatch = alice.MustSyncUntil(
			t,
			client.SyncReq{Since: aliceNextBatch},
			client.SyncJoinedTo(bob.UserID, roomID),
			syncDeviceListsHas("changed", bob.UserID),
		)
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)
		// Some homeservers (Synapse) may emit another `changed` update after querying keys.
		aliceNextBatch = barrier(t, aliceNextBatch)

		// Both homeservers think Bob has joined now
		// Bob then updates their device list
		t.Logf("%s updates their device list.", bob.UserID)
		checkBobKeys = uploadNewKeys(t, bob)

		// Check that Alice receives a device list update from Bob
		t.Logf("%s expects a device list change for %s...", alice.UserID, bob.UserID)
		alice.MustSyncUntil(
			t,
			client.SyncReq{Since: aliceNextBatch},
			syncDeviceListsHas("changed", bob.UserID),
		)
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)
	}

	// testJoin tests Alice joining a room another user is already in.
	testJoin := func(
		t *testing.T, deployment complement.Deployment, hsName string, otherHSName string,
	) {
		alice := deployment.Register(t, hsName, helpers.RegistrationOpts{
			Localpart: generateLocalpart("alice"),
			Password:  "password",
		})
		bob := deployment.Register(t, otherHSName, helpers.RegistrationOpts{
			Localpart: generateLocalpart("bob"),
			Password:  "password",
		})
		barrier := makeBarrier(t, deployment, alice, otherHSName)
		checkBobKeys := uploadNewKeys(t, bob)

		roomID := bob.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
		t.Logf("%s created test room %s.", bob.UserID, roomID)

		// Alice performs an initial sync
		_, aliceNextBatch := alice.MustSync(t, client.SyncReq{})

		// Alice joins the room
		t.Logf("%s joins the test room.", alice.UserID)
		alice.MustJoinRoom(t, roomID, []string{otherHSName})
		bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

		// Check that Alice receives a device list update from Bob
		t.Logf("%s expects a device list change for %s...", alice.UserID, bob.UserID)
		aliceNextBatch = alice.MustSyncUntil(
			t,
			client.SyncReq{Since: aliceNextBatch},
			client.SyncJoinedTo(alice.UserID, roomID),
			syncDeviceListsHas("changed", bob.UserID),
		)
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)
		// Some homeservers (Synapse) may emit another `changed` update after querying keys.
		aliceNextBatch = barrier(t, aliceNextBatch)

		// Both homeservers think Alice has joined now
		// Bob then updates their device list
		t.Logf("%s updates their device list.", bob.UserID)
		checkBobKeys = uploadNewKeys(t, bob)

		// Check that Alice receives a device list update from Bob
		t.Logf("%s expects a device list change for %s...", alice.UserID, bob.UserID)
		alice.MustSyncUntil(
			t,
			client.SyncReq{Since: aliceNextBatch},
			syncDeviceListsHas("changed", bob.UserID),
		)
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)
	}

	// testOtherUserLeave tests another user leaving a room Alice is in.
	testOtherUserLeave := func(t *testing.T, deployment complement.Deployment, hsName string, otherHSName string) {
		alice := deployment.Register(t, hsName, helpers.RegistrationOpts{
			Localpart: generateLocalpart("alice"),
			Password:  "password",
		})
		bob := deployment.Register(t, otherHSName, helpers.RegistrationOpts{
			Localpart: generateLocalpart("bob"),
			Password:  "password",
		})
		barrier := makeBarrier(t, deployment, alice, otherHSName)
		checkBobKeys := uploadNewKeys(t, bob)

		roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
		t.Logf("%s created test room %s.", alice.UserID, roomID)

		// Bob joins the room
		t.Logf("%s joins the test room.", bob.UserID)
		bob.MustJoinRoom(t, roomID, []string{hsName})
		bobNextBatch := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

		// Alice performs an initial sync
		aliceNextBatch := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)
		// Some homeservers (Synapse) may emit another `changed` update after querying keys.
		aliceNextBatch = barrier(t, aliceNextBatch)

		// Bob leaves the room
		t.Logf("%s leaves the test room.", bob.UserID)
		bob.MustLeaveRoom(t, roomID)
		bob.MustSyncUntil(t, client.SyncReq{Since: bobNextBatch}, client.SyncLeftFrom(bob.UserID, roomID))

		// Check that Alice is notified that she will no longer receive updates about Bob's devices
		t.Logf("%s expects a device list left for %s...", alice.UserID, bob.UserID)
		aliceNextBatch = alice.MustSyncUntil(
			t,
			client.SyncReq{Since: aliceNextBatch},
			client.SyncLeftFrom(bob.UserID, roomID),
			syncDeviceListsHas("left", bob.UserID),
		)

		// Both homeservers think Bob has left now
		// Bob then updates their device list
		// Alice's homeserver is not expected to get the device list update and must not return a
		// cached device list for Bob.
		t.Logf("%s updates their device list.", bob.UserID)
		checkBobKeys = uploadNewKeys(t, bob)
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)

		// Check that Alice is not notified about Bob's device update
		t.Logf("%s expects no device list change for %s...", alice.UserID, bob.UserID)
		syncResult, _ := alice.MustSync(t, client.SyncReq{Since: aliceNextBatch})
		if syncDeviceListsHas("changed", bob.UserID)(alice.UserID, syncResult) == nil {
			t.Fatalf("Alice was unexpectedly notified about Bob's device update even though they share no rooms")
		}
	}

	// testLeave tests Alice leaving a room another user is in.
	testLeave := func(t *testing.T, deployment complement.Deployment, hsName string, otherHSName string) {
		alice := deployment.Register(t, hsName, helpers.RegistrationOpts{
			Localpart: generateLocalpart("alice"),
			Password:  "password",
		})
		bob := deployment.Register(t, otherHSName, helpers.RegistrationOpts{
			Localpart: generateLocalpart("bob"),
			Password:  "password",
		})
		barrier := makeBarrier(t, deployment, alice, otherHSName)
		checkBobKeys := uploadNewKeys(t, bob)

		roomID := bob.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
		t.Logf("%s created test room %s.", bob.UserID, roomID)

		// Alice joins the room
		t.Logf("%s joins the test room.", alice.UserID)
		alice.MustJoinRoom(t, roomID, []string{otherHSName})
		bobNextBatch := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

		// Alice performs an initial sync
		aliceNextBatch := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)
		// Some homeservers (Synapse) may emit another `changed` update after querying keys.
		aliceNextBatch = barrier(t, aliceNextBatch)

		// Alice leaves the room
		t.Logf("%s leaves the test room.", alice.UserID)
		alice.MustLeaveRoom(t, roomID)
		bob.MustSyncUntil(t, client.SyncReq{Since: bobNextBatch}, client.SyncLeftFrom(alice.UserID, roomID))

		// Check that Alice is notified that she will no longer receive updates about Bob's devices
		t.Logf("%s expects a device list left for %s...", alice.UserID, bob.UserID)
		aliceNextBatch = alice.MustSyncUntil(
			t,
			client.SyncReq{Since: aliceNextBatch},
			client.SyncLeftFrom(alice.UserID, roomID),
			syncDeviceListsHas("left", bob.UserID),
		)

		// Both homeservers think Alice has left now
		// Bob then updates their device list
		// Alice's homeserver is not expected to get the device list update and must not return a
		// cached device list for Bob.
		t.Logf("%s updates their device list.", bob.UserID)
		checkBobKeys = uploadNewKeys(t, bob)
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)

		// Check that Alice is not notified about Bob's device update
		t.Logf("%s expects no device list change for %s...", alice.UserID, bob.UserID)
		syncResult, _ := alice.MustSync(t, client.SyncReq{Since: aliceNextBatch})
		if syncDeviceListsHas("changed", bob.UserID)(alice.UserID, syncResult) == nil {
			t.Fatalf("Alice was unexpectedly notified about Bob's device update even though they share no rooms")
		}
	}

	// testOtherUserRejoin tests another user leaving and rejoining a room Alice is in.
	testOtherUserRejoin := func(t *testing.T, deployment complement.Deployment, hsName string, otherHSName string) {
		alice := deployment.Register(t, hsName, helpers.RegistrationOpts{
			Localpart: generateLocalpart("alice"),
			Password:  "password",
		})
		bob := deployment.Register(t, otherHSName, helpers.RegistrationOpts{
			Localpart: generateLocalpart("bob"),
			Password:  "password",
		})
		barrier := makeBarrier(t, deployment, alice, otherHSName)
		checkBobKeys := uploadNewKeys(t, bob)

		roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
		t.Logf("%s created test room %s.", alice.UserID, roomID)

		// Bob joins the room
		t.Logf("%s joins the test room.", bob.UserID)
		bob.MustJoinRoom(t, roomID, []string{hsName})
		bobNextBatch := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

		// Alice performs an initial sync
		aliceNextBatch := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)
		// Some homeservers (Synapse) may emit another `changed` update after querying keys.
		aliceNextBatch = barrier(t, aliceNextBatch)

		// Both homeservers think Bob has joined now
		// Bob leaves the room
		t.Logf("%s leaves the test room.", bob.UserID)
		bob.MustLeaveRoom(t, roomID)
		bobNextBatch = bob.MustSyncUntil(t, client.SyncReq{Since: bobNextBatch}, client.SyncLeftFrom(bob.UserID, roomID))

		// Check that Alice is notified that she will no longer receive updates about Bob's devices
		t.Logf("%s expects a device list left for %s...", alice.UserID, bob.UserID)
		alice.MustSyncUntil(
			t,
			client.SyncReq{Since: aliceNextBatch},
			client.SyncLeftFrom(bob.UserID, roomID),
			syncDeviceListsHas("left", bob.UserID),
		)

		// Both homeservers think Bob has left now
		// Bob then updates their device list before rejoining the room
		// Alice's homeserver is not expected to get the device list update.
		t.Logf("%s updates their device list.", bob.UserID)
		checkBobKeys = uploadNewKeys(t, bob)

		// Bob rejoins the room
		t.Logf("%s joins the test room.", bob.UserID)
		bob.MustJoinRoom(t, roomID, []string{hsName})
		bob.MustSyncUntil(t, client.SyncReq{Since: bobNextBatch}, client.SyncJoinedTo(bob.UserID, roomID))

		// Check that Alice is notified that Bob's devices have a change
		// Alice's homeserver must not return a cached device list for Bob.
		t.Logf("%s expects a device list change for %s...", alice.UserID, bob.UserID)
		alice.MustSyncUntil(
			t,
			client.SyncReq{Since: aliceNextBatch},
			client.SyncJoinedTo(bob.UserID, roomID),
			syncDeviceListsHas("changed", bob.UserID),
		)
		mustQueryKeys(t, alice, bob.UserID, checkBobKeys)
	}

	// Create two homeservers
	// The users and rooms in the blueprint won't be used.
	// Each test creates their own Alice and Bob users.
	deployment := complement.Deploy(t, b.BlueprintFederationOneToOneRoom)
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
