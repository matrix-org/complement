package csapi_tests

import (
	"net/http"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestRoomLevels(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")

	successCount := 0
	defer func() {
		// sytest: Both GET and PUT work
		if successCount != 2 {
			t.Fatalf("expected GET and PUT to work")
		}
	}()
	t.Run("Parallel", func(t *testing.T) {
		// sytest: GET /rooms/:room_id/state/m.room.power_levels can fetch levels
		t.Run("GET /rooms/:room_id/state/m.room.power_levels can fetch levels", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
			res := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.power_levels"})

			body := gjson.ParseBytes(must.ParseJSON(t, res.Body))
			requiredFields := []string{"ban", "kick", "redact", "state_default", "events_default", "events", "users"}
			for i := range requiredFields {
				if !body.Get(requiredFields[i]).Exists() {
					t.Fatalf("expected json field %s, but it does not exist", requiredFields[i])
				}
			}
			users := body.Get("users").Map()
			alicePowerLevel, ok := users[alice.UserID]
			if !ok {
				t.Fatalf("Expected room creator (%s) to exist in user powerlevel list", alice.UserID)
			}

			userDefaults := body.Get("user_defaults").Int()

			if userDefaults > alicePowerLevel.Int() {
				t.Fatalf("Expected room creator to have a higher-than-default powerlevel")
			}
			successCount++
		})
		// sytest: PUT /rooms/:room_id/state/m.room.power_levels can set levels
		t.Run("PUT /rooms/:room_id/state/m.room.power_levels can set levels", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
			res := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.power_levels"})

			powerLevels := gjson.ParseBytes(must.ParseJSON(t, res.Body))
			changedUser := client.GjsonEscape("@random-other-user:their.home")
			alicePowerLevel := powerLevels.Get("users." + alice.UserID).Int()
			pl := map[string]int64{
				alice.UserID:                    alicePowerLevel,
				"@random-other-user:their.home": 20,
			}
			newPowerlevels, err := sjson.Set(powerLevels.Str, "users", pl)
			if err != nil {
				t.Fatalf("unable to update powerlevel JSON")
			}
			reqBody := client.WithRawBody([]byte(newPowerlevels))
			alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.power_levels"}, reqBody)
			res = alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.power_levels"})
			powerLevels = gjson.ParseBytes(must.ParseJSON(t, res.Body))
			if powerLevels.Get("users."+changedUser).Int() != 20 {
				t.Fatal("Expected to have set other user's level to 20")
			}
			successCount++
		})
		// sytest: PUT power_levels should not explode if the old power levels were empty
		t.Run("PUT power_levels should not explode if the old power levels were empty", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"users": map[string]int64{
					alice.UserID: 100,
				},
			})
			alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.power_levels"}, reqBody)
			// absence of a 'users' key
			reqBody = client.WithJSONBody(t, map[string]interface{}{})
			alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.power_levels"}, reqBody)
			// this should now give a 403 (not a 500)
			res := alice.DoFunc(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.power_levels"}, reqBody)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusForbidden,
			})
		})
	})
}
