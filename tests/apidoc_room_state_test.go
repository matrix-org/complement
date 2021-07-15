package tests

import (
	"net/url"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestRoomState(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	authedClient := deployment.Client(t, "hs1", "@alice:hs1")
	t.Run("Parallel", func(t *testing.T) {
		// sytest: GET /rooms/:room_id/state/m.room.member/:user_id fetches my membership
		t.Run("GET /rooms/:room_id/state/m.room.member/:user_id fetches my membership", func(t *testing.T) {
			t.Parallel()
			roomID := authedClient.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
			})
			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "state", "m.room.member", authedClient.UserID})
			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("membership"),
					match.JSONKeyTypeEqual("membership", gjson.String),
					match.JSONKeyEqual("membership", "join"),
				},
			})
		})
		// sytest: GET /rooms/:room_id/state/m.room.member/:user_id?format=event fetches my membership event
		t.Run("GET /rooms/:room_id/state/m.room.member/:user_id?format=event fetches my membership event", func(t *testing.T) {
			t.Parallel()
			queryParams := url.Values{}
			queryParams.Set("format", "event")
			roomID := authedClient.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
			})
			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "state", "m.room.member", authedClient.UserID}, client.WithQueries(queryParams))
			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("sender"),
					match.JSONKeyPresent("room_id"),
					match.JSONKeyPresent("content"),
					match.JSONKeyEqual("sender", authedClient.UserID),
					match.JSONKeyEqual("room_id", roomID),
					match.JSONKeyEqual("content.membership", "join"),
				},
			})
		})
		// sytest: GET /rooms/:room_id/state/m.room.power_levels fetches powerlevels
		t.Run("GET /rooms/:room_id/state/m.room.power_levels fetches powerlevels", func(t *testing.T) {
			t.Parallel()
			roomID := authedClient.CreateRoom(t, map[string]interface{}{
				"visibility":      "public",
				"preset":          "public_chat",
				"room_alias_name": "room_alias",
			})
			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "state", "m.room.power_levels"})
			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("ban"),
					match.JSONKeyPresent("kick"),
					match.JSONKeyPresent("redact"),
					match.JSONKeyPresent("users_default"),
					match.JSONKeyPresent("state_default"),
					match.JSONKeyPresent("events_default"),
					match.JSONKeyPresent("users"),
					match.JSONKeyPresent("events"),
				},
			})
		})
	})
}
