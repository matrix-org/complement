//go:build msc2477
// +build msc2477

package tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

const TEST_ROOM_VERSION = "org.matrix.msc2477"

// * Test sending an ephemeral event and receiving it down /sync
// * Test that ephemeral events are rejected by the appropriate power levels
func TestUserDefinedEphemeralEvents(t *testing.T) {
	// TODO: After defining a rate limit on the homeserver, we'll need to remove it for Complement
	deployment := Deploy(t, b.BlueprintFederationTwoLocalOneRemote)
	defer deployment.Destroy(t)

	// Create a client for one local user
	alice := deployment.Client(t, "hs1", "@alice:hs1")

	// Create a client for another local user
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	// TODO: Check that a remote user receives user-defined EDUs sent in the room
	// TODO: Check that an appservice interested in one of the users receives the EDU

	// Create a room for the users
	roomID := alice.CreateRoom(t, struct {
		Preset                    string         `json:"preset"`
		RoomVersion               string         `json:"room_version"`
		PowerLevelContentOverride map[string]int `json:"power_level_content_override"`
	}{
		"public_chat",     // Allow anyone to join the room
		TEST_ROOM_VERSION, // A room version that supports user-defined EDUs
		map[string]int{
			// Allow any user to send ephemeral events.
			// We explicitly do so to ensure that the permissions check
			// below is receiving a 403 due to not being in the room,
			// rather than failing a power levels check.
			"ephemeral_default": 0,
		},
	})

	test_ephemeral_event_type := "todays.best.pony"
	test_ephemeral_event_body := []byte(`{
		"best_pony": "donut steel",
		"age": 5,
		"characteristics": {
			"mane": "maroon",
			"tail": "teal"
		}
	}`)

	// Bob should not be able to send a user-defined EDU to the room without first
	// having joined it
	t.Run("Sending a user-defined EDU to a room you are not joined to should raise a permission error", func(t *testing.T) {
		// Bob tries to send a user-defined EDU to the room.
		// This should fail, as Bob is not in the room.
		res := bob.DoFunc(
			t,
			"PUT",
			[]string{
				"_matrix", "client", "v1", "rooms", roomID, "ephemeral", "org.matrix.msc2477", test_ephemeral_event_type, "1",
			},
			client.WithRawBody([]byte(test_ephemeral_event_body)),
		)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 403,
		})
	})

	// Raise the power level required to send ephemeral events again
	empty_state_key := ""
	alice.SendEventSynced(t, roomID, b.Event{
		Type:     "m.room.power_levels",
		StateKey: &empty_state_key,
		Content: map[string]interface{}{
			"ephemeral_default": 50,
			"users": map[string]interface{}{
				alice.UserID: 100,
			},
		},
	})

	// Have Bob join the room
	bob.JoinRoom(t, roomID, nil)

	// Bob shouldn't be able to send user-defined ephemeral event due to too low of a power level
	t.Run("A user with insufficient permission should receive an error on sending an ephemeral event", func(t *testing.T) {
		res := bob.DoFunc(
			t,
			"PUT",
			[]string{
				"_matrix", "client", "v1", "rooms", roomID, "ephemeral", "org.matrix.msc2477", test_ephemeral_event_type, "2",
			},
			client.WithRawBody([]byte(test_ephemeral_event_body)),
		)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 403,
		})
	})

	// TODO: A user joining the room should not receive ephemeral events that were sent before they joined
	// TODO: Test transactions are idempotent

	// Raise Bob's power level to allow them to send user-defined EDUs
	alice.SendEventSynced(t, roomID, b.Event{
		Type:     "m.room.power_levels",
		StateKey: &empty_state_key,
		Content: map[string]interface{}{
			"ephemeral_default": 50,
			"users": map[string]interface{}{
				alice.UserID: 100,
				bob.UserID:   50,
			},
		},
	})

	t.Run("Sent user-defined EDUs should appear in other user's sync streams", func(t *testing.T) {
		bob.MustDoFunc(
			t,
			"PUT",
			[]string{
				"_matrix", "client", "v1", "rooms", roomID, "ephemeral", "org.matrix.msc2477", test_ephemeral_event_type, "3",
			},
			client.WithRawBody([]byte(test_ephemeral_event_body)),
		)

		alice.MustSyncUntil(t, client.SyncReq{})
		// Check Alice's sync stream
		// Are we waiting for Bob above...?

		// Alice should not receive the same ephemeral event if they sync again
	})

	// TODO: Should ephemeral events *never* appear in initial sync requests?
	// This would prevent client just logging in from seeing ephemeral events sent beforehand

	// Now Alice raises the power level required to send Bob's specific event type.
	alice.SendEventSynced(t, roomID, b.Event{
		Type:     "m.room.power_levels",
		StateKey: &empty_state_key,
		Content: map[string]interface{}{
			"ephemeral": map[string]interface{}{
				test_ephemeral_event_type: 100,
			},
			"ephemeral_default": 50,
			"users": map[string]interface{}{
				alice.UserID: 100,
				bob.UserID:   50,
			},
		},
	})

	// Bob should no longer be able to send this event type
	t.Run("A user should not be able to send a user-defined EDU with a specific type that requires too high a power level", func(t *testing.T) {
		// Bob tries and fails to send the same event type from before. Their power level is
		// too low (50 < 100).
		res := bob.DoFunc(
			t,
			"PUT",
			[]string{
				"_matrix", "client", "v1", "rooms", roomID, "ephemeral", "org.matrix.msc2477", test_ephemeral_event_type, "4",
			},
			client.WithRawBody([]byte(test_ephemeral_event_body)),
		)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 403,
		})
	})
}
