package tests

import (
	"math/big"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestClientSendingPowerLevelsEvents(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAliceAndBob)
	defer deployment.Destroy(t)

	emptyStateKey := ""

	// Create a client for one local user
	aliceUserID := "@alice:hs1"
	alice := deployment.Client(t, "hs1", aliceUserID)

	// Create a client for another local user
	bobUserID := "@bob:hs1"
	bob := deployment.Client(t, "hs1", bobUserID)

	t.Run("Attempt to set valid power levels event", func(t *testing.T) {
		roomID := alice.CreateRoom(t, map[string]interface{}{
			"preset": "public_chat",
		})

		alice.SendEventSynced(t, roomID, b.Event{
			Type:     "m.room.power_levels",
			Sender:   alice.UserID,
			StateKey: &emptyStateKey,
			Content: map[string]interface{}{
				"ban": 50,
				"events": map[string]interface{}{
					"m.room.name":         100,
					"m.room.power_levels": 100,
				},
				"events_default": 0,
				"invite":         50,
				"kick":           50,
				"notifications": map[string]interface{}{
					"room": 20,
				},
				"redact":        50,
				"state_default": 50,
				"users": map[string]interface{}{
					alice.UserID: 100,
					bob.UserID:   0,
				},
				"users_default": 0,
			},
		})
	})

	t.Run("Attempt to set power levels event with string power level fails", func(t *testing.T) {
		roomID := alice.CreateRoom(t, map[string]interface{}{
			"preset": "public_chat",
		})

		res := alice.SendEvent(t, roomID, b.Event{
			Type:     "m.room.power_levels",
			Sender:   alice.UserID,
			StateKey: &emptyStateKey,
			Content: map[string]interface{}{
				"users_default": "30",
				"users": map[string]interface{}{
					alice.UserID: 100,
					bob.UserID:   0,
				},
				"events": map[string]interface{}{
					"m.room.power_levels": 100,
				},
			},
		})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 400,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_BAD_JSON"),
			},
		})
	})

	t.Run("Attempt to set power levels event with unsafe integer fails", func(t *testing.T) {
		roomID := alice.CreateRoom(t, map[string]interface{}{
			"preset":       "public_chat",
			"room_version": "5", // room version from before canonical JSON was enforced
		})

		var smallnum, _ = new(big.Int).SetString("-9007199254740992", 0)
		res := alice.SendEvent(t, roomID, b.Event{
			Type:     "m.room.power_levels",
			Sender:   alice.UserID,
			StateKey: &emptyStateKey,
			Content: map[string]interface{}{
				"users_default": smallnum,
			},
		})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 400,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_BAD_JSON"),
			},
		})

		var bignum, _ = new(big.Int).SetString("9007199254740992", 0)
		res2 := alice.SendEvent(t, roomID, b.Event{
			Type:     "m.room.power_levels",
			Sender:   alice.UserID,
			StateKey: &emptyStateKey,
			Content: map[string]interface{}{
				"users_default": bignum,
			},
		})
		must.MatchResponse(t, res2, match.HTTPResponse{
			StatusCode: 400,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_BAD_JSON"),
			},
		})
	})
}
