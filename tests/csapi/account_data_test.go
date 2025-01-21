package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

func TestAddAccountData(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	// sytest: Can add account data
	// sytest: Can get account data without syncing
	t.Run("Can add global account data", func(t *testing.T) {
		// Set the account data entry
		alice.MustSetGlobalAccountData(t, "test.key", map[string]interface{}{"value": "first"})

		// check that getting the account data returns the correct value
		must.MatchResponse(t, alice.MustGetGlobalAccountData(t, "test.key"), match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("value", "first"),
			},
		})

		// Set it to something else
		alice.MustSetGlobalAccountData(t, "test.key", map[string]interface{}{"value": "second"})

		// check that getting the account data returns the updated value
		must.MatchResponse(t, alice.MustGetGlobalAccountData(t, "test.key"), match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("value", "second"),
			},
		})
	})

	// sytest: Can add account data to room
	// sytest: Can get room account data without syncing
	t.Run("Can add room account data", func(t *testing.T) {
		// Create a room
		roomID := alice.MustCreateRoom(t, map[string]interface{}{})

		// Set the room account data entry
		alice.MustSetRoomAccountData(t, roomID, "test.key", map[string]interface{}{"value": "room first"})

		// check that getting the account data returns the correct value
		must.MatchResponse(t, alice.MustGetRoomAccountData(t, roomID, "test.key"), match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("value", "room first"),
			},
		})

		// Set it to something else
		alice.MustSetRoomAccountData(t, roomID, "test.key", map[string]interface{}{"value": "room second"})

		// check that getting the account data returns the updated value
		must.MatchResponse(t, alice.MustGetRoomAccountData(t, roomID, "test.key"), match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("value", "room second"),
			},
		})
	})
}
