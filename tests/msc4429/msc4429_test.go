package tests

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
)

const (
	// TODO: Support stable prefix once MSC4429 is accepted.
	// msc4429UsersStable   = "users"
	msc4429UsersUnstable = "org\\.matrix\\.msc4429\\.users"
)

func TestMSC4429ProfileUpdates(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	t.Run("Initial sync includes requested profile fields and filters others", func(t *testing.T) {
		alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "alice-initial"})
		bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "bob-initial"})

		mustCreateSharedRoom(t, alice, bob)

		// Bob sets their displayname.
		bob.MustSetDisplayName(t, "Bob Display")
		// Bob sets their status.
		mustSetProfileField(t, bob, "m.status", map[string]interface{}{
			"text":  "busy",
			"emoji": "🛑",
		})

		// Alice /sync's, but only asks for "m.status" changes.
		// Exclude 'displayname'.
		filter := mustBuildMSC4429Filter(t, []string{"m.status"})
		res, _ := alice.MustSync(t, client.SyncReq{Filter: filter})

		// We should see the m.status profile update.
		alice.MustSyncUntil(t, client.SyncReq{Filter: filter}, syncHasProfileUpdate(bob.UserID, "m.status", map[string]interface{}{
			"text":  "busy",
			"emoji": "🛑",
		}))

		// We should NOT see a displayname profile update.
		assertNoProfileUpdate(t, res, bob.UserID, "displayname")
	})

	// Receiving profile updates are an opt-in mechanism, according to MSC4429.
	t.Run("No updates without profile_fields filter", func(t *testing.T) {
		alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "alice-nofilter"})
		bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "bob-nofilter"})
		charlie := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "charlie-nofilter"})

		mustCreateSharedRoom(t, alice, bob, charlie)

		// Perform an initial sync to get a since token.
		_, since := alice.MustSync(t, client.SyncReq{})

		// Bob sets their status.
		mustSetProfileField(t, bob, "m.status", map[string]interface{}{
			"text":  "away",
			"emoji": "🟡",
		})

		// Assert that charlie receives bob's profile update in an incremental sync
		// with the appropriate filter set.
		filter := mustBuildMSC4429Filter(t, []string{"m.status"})
		charlie.MustSyncUntil(t, client.SyncReq{Filter: filter}, syncHasProfileUpdate(bob.UserID, "m.status", map[string]interface{}{
			"text":  "away",
			"emoji": "🟡",
		}))

		// Assert that alice does not receive the profile update in an incremental sync.
		res, _ := alice.MustSync(t, client.SyncReq{Since: since})
		assertNoProfileUpdate(t, res, bob.UserID, "m.status")
	})

	// Check that only the latest update is returned per-user per-field.
	t.Run("Incremental sync returns the latest update", func(t *testing.T) {
		alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "alice-latest"})
		bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "bob-latest"})

		mustCreateSharedRoom(t, alice, bob)

		filter := mustBuildMSC4429Filter(t, []string{"m.status"})
		_, since := alice.MustSync(t, client.SyncReq{Filter: filter})

		mustSetProfileField(t, bob, "m.status", map[string]interface{}{
			"text": "first",
		})
		mustSetProfileField(t, bob, "m.status", map[string]interface{}{
			"text": "second",
		})

		alice.MustSyncUntil(
			t,
			client.SyncReq{Since: since, Filter: filter},
			syncHasProfileUpdate(bob.UserID, "m.status", map[string]interface{}{
				"text": "second",
			}),
		)
	})

	// Test that the homeserver informs the client when a profile field is cleared.
	t.Run("Cleared profile field is returned as null", func(t *testing.T) {
		alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "alice-clear"})
		bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "bob-clear"})

		mustCreateSharedRoom(t, alice, bob)

		filter := mustBuildMSC4429Filter(t, []string{"m.status"})
		_, since := alice.MustSync(t, client.SyncReq{Filter: filter})

		// Bob sets a status.
		mustSetProfileField(t, bob, "m.status", map[string]interface{}{
			"text": "busy",
		})

		// Wait until alice can see the status.
		since = alice.MustSyncUntil(
			t,
			client.SyncReq{Since: since, Filter: filter},
			syncHasProfileUpdate(bob.UserID, "m.status", map[string]interface{}{
				"text": "busy",
			}),
		)

		// Bob clears their status.
		mustSetProfileField(t, bob, "m.status", nil)

		// Wait until alice sees the status be set to `null` (nil).
		alice.MustSyncUntil(
			t,
			client.SyncReq{Since: since, Filter: filter},
			syncHasProfileUpdate(bob.UserID, "m.status", nil),
		)
	})

	t.Run("A user leaving the last shared room returns a profile update of null", func(t *testing.T) {
		alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "alice-leave"})
		bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "bob-leave"})

		roomID := mustCreateSharedRoom(t, alice, bob)

		filter := mustBuildMSC4429Filter(t, []string{"m.status"})
		since := alice.MustSyncUntil(
			t,
			client.SyncReq{Filter: filter},
			client.SyncJoinedTo(alice.UserID, roomID),
		)

		// Bob sets a status while Alice and Bob share a room.
		mustSetProfileField(t, bob, "m.status", map[string]interface{}{
			"text": "busy",
		})

		// Alice receives Bob's changed profile field.
		since = alice.MustSyncUntil(
			t,
			client.SyncReq{Since: since, Filter: filter},
			syncHasProfileUpdate(bob.UserID, "m.status", map[string]interface{}{
				"text": "busy",
			}),
		)

		// Bob leaves the only room shared with Alice.
		bob.MustLeaveRoom(t, roomID)

		// Alice receives a null profile_updates value for Bob. This tells
		// clients to clear their local cache for Bob's profile.
		alice.MustSyncUntil(
			t,
			client.SyncReq{Since: since, Filter: filter},
			// Check that bob left the room and we get a null profile field for them.
			//
			// `MustSyncUntil` will loop until both checks are true. That
			// doesn't necessarily have to happen in the same sync response.
			client.SyncLeftFrom(bob.UserID, roomID),
			syncHasProfileUpdatesNull(bob.UserID),
		)
	})
}

// mustBuildMSC4429Filter builds a filter that can be used to limit the field
// IDs returned in a `/sync` response.
func mustBuildMSC4429Filter(t *testing.T, ids []string) string {
	t.Helper()
	filter := map[string]interface{}{
		"profile_fields": map[string]interface{}{
			"ids": ids,
		},
		"org.matrix.msc4429.profile_fields": map[string]interface{}{
			"ids": ids,
		},
	}
	encoded, err := json.Marshal(filter)
	if err != nil {
		t.Fatalf("failed to marshal MSC4429 filter: %s", err)
	}
	return string(encoded)
}

// mustCreateSharedRoom creates a shared room between `alice` and `bob` and returns the
// room ID.
func mustCreateSharedRoom(t *testing.T, users ...*client.CSAPI) string {
	t.Helper()

	// Use one of the users to create the room.
	roomID := users[0].MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	// Join all of the given users to the room.
	for _, user := range users {
		user.MustJoinRoom(t, roomID, nil)
		user.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(user.UserID, roomID))
	}

	return roomID
}

// mustSetProfileField sets the given profile field ID to the given value on the given user's
// profile.
func mustSetProfileField(t *testing.T, user *client.CSAPI, field string, value interface{}) {
	t.Helper()
	user.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "profile", user.UserID, field},
		client.WithJSONBody(t, map[string]interface{}{
			field: value,
		}),
	)
}

// getProfileUpdate extracts the given profile updates for a given user by field
// ID from a legacy `/sync` response.
func getProfileUpdate(res gjson.Result, userID, field string) (gjson.Result, bool) {
	unstablePath := msc4429UsersUnstable + "." + client.GjsonEscape(userID) + ".profile_updates." + client.GjsonEscape(field)
	unstableRes := res.Get(unstablePath)
	if unstableRes.Exists() {
		return unstableRes, true
	}
	return gjson.Result{}, false
}

// getProfileUpdates extracts all profile updates for a given user from a legacy
// `/sync` response.
func getProfileUpdates(res gjson.Result, userID string) (gjson.Result, bool) {
	unstablePath := msc4429UsersUnstable + "." + client.GjsonEscape(userID) + ".profile_updates"
	unstableRes := res.Get(unstablePath)
	if unstableRes.Exists() {
		return unstableRes, true
	}
	return gjson.Result{}, false
}

// assertNoProfileUpdate asserts that a user has not updated a field of their
// profile in the given legacy /sync response JSON.
func assertNoProfileUpdate(t *testing.T, res gjson.Result, userID, field string) {
	t.Helper()
	if update, ok := getProfileUpdate(res, userID, field); ok {
		t.Fatalf("expected no profile update for %s %s: %s", userID, field, update.Raw)
	}
}

// syncHasProfileUpdate checks whether a given sync response contains a profile
// update of the given, expected field and value.
func syncHasProfileUpdate(userID, field string, expected interface{}) client.SyncCheckOpt {
	return func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		update, ok := getProfileUpdate(topLevelSyncJSON, userID, field)
		if !ok {
			return fmt.Errorf("missing profile update for %s %s", userID, field)
		}
		if expected == nil {
			if update.Type != gjson.Null {
				return fmt.Errorf("expected null profile update for %s %s, got %s", userID, field, update.Type)
			}
			return nil
		}
		if err := match.JSONKeyEqual("", expected)(update); err != nil {
			return fmt.Errorf("profile update mismatch for %s %s: %w", userID, field, err)
		}
		return nil
	}
}

// syncHasProfileUpdatesNull checks whether a sync response contains a null
// profile_updates value for the given user.
func syncHasProfileUpdatesNull(userID string) client.SyncCheckOpt {
	return func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		updates, ok := getProfileUpdates(topLevelSyncJSON, userID)
		if !ok {
			return fmt.Errorf("missing profile updates for %s", userID)
		}
		if updates.Type != gjson.Null {
			return fmt.Errorf("expected a null profile update for %s, got %s", userID, updates.Type)
		}
		return nil
	}
}
