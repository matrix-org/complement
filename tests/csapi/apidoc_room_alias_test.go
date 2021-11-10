package csapi_tests

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestRoomAlias(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	authedClient := deployment.Client(t, "hs1", "@alice:hs1")
	t.Run("Parallel", func(t *testing.T) {
		// sytest: PUT /directory/room/:room_alias creates alias
		t.Run("PUT /directory/room/:room_alias creates alias", func(t *testing.T) {
			t.Parallel()
			roomID := authedClient.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
			})

			roomAlias := "#room_alias_test:hs1"

			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"room_id": roomID,
			})

			_ = authedClient.MustDoFunc(t, "PUT", []string{"_matrix", "client", "r0", "directory", "room", roomAlias}, reqBody)

			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "directory", "room", roomAlias})

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("room_id"),
					match.JSONKeyTypeEqual("room_id", gjson.String),
					match.JSONKeyEqual("room_id", roomID),
				},
			})
		})
		// sytest: GET /rooms/:room_id/aliases lists aliases
		t.Run("GET /rooms/:room_id/aliases lists aliases", func(t *testing.T) {
			t.Parallel()
			roomID := authedClient.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
			})

			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "aliases"})

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("aliases"),
					match.JSONKeyEqual("aliases", []interface{}{}),
				},
			})

			roomAlias := "#room_alias:hs1"

			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"room_id": roomID,
			})

			_ = authedClient.MustDoFunc(t, "PUT", []string{"_matrix", "client", "r0", "directory", "room", roomAlias}, reqBody)

			res = authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "aliases"})

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("aliases"),
					match.JSONKeyEqual("aliases", []interface{}{roomAlias}),
				},
			})
		})
	})
}
