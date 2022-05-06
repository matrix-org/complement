package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
)

// This test ensures that an authorised (PL 100) user is able to modify the users_default value
// when that value is equal to the value of authorised user.
// Regression test for https://github.com/matrix-org/gomatrixserverlib/pull/306
func TestDemotingUsersViaUsersDefault(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"power_level_content_override": map[string]interface{}{
			"users_default": 100, // the default is 0
			"users": map[string]interface{}{
				alice.UserID: 100,
			},
			"events":        map[string]int64{},
			"notifications": map[string]int64{},
		},
	})

	alice.SendEventSynced(t, roomID, b.Event{
		Type:     "m.room.power_levels",
		StateKey: b.Ptr(""),
		Content: map[string]interface{}{
			"users_default": 40, // we change the default to 40. We should be able to do this.
			"users": map[string]interface{}{
				alice.UserID: 100,
			},
			"events":        map[string]int64{},
			"notifications": map[string]int64{},
		},
	})
}
