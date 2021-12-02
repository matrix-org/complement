package csapi_tests

import (
	"net/http"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func setRoomAliasResp(t *testing.T, c *client.CSAPI, roomID, roomAlias string) *http.Response {
	return c.MustDoFunc(t, "PUT", []string{"_matrix", "client", "r0", "directory", "room", roomAlias}, client.WithJSONBody(t, map[string]interface{}{
		"room_id": roomID,
	}))
}

func getRoomAliasResp(t *testing.T, c *client.CSAPI, roomAlias string) *http.Response {
	return c.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "directory", "room", roomAlias})
}

func listRoomAliasesResp(t *testing.T, c *client.CSAPI, roomID string) *http.Response {
	return c.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "aliases"})
}

func TestRoomAlias(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	t.Run("Parallel", func(t *testing.T) {

		// sytest: PUT /directory/room/:room_alias creates alias
		t.Run("PUT /directory/room/:room_alias creates alias", func(t *testing.T) {
			t.Parallel()

			roomID := alice.CreateRoom(t, map[string]interface{}{})

			roomAlias := "#room_alias_test:hs1"

			setRoomAliasResp(t, alice, roomID, roomAlias)

			res := getRoomAliasResp(t, alice, roomAlias)

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyEqual("room_id", roomID),
				},
			})
		})

		// sytest: GET /rooms/:room_id/aliases lists aliases
		t.Run("GET /rooms/:room_id/aliases lists aliases", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{})

			res := listRoomAliasesResp(t, alice, roomID)

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyEqual("aliases", []interface{}{}),
				},
			})

			roomAlias := "#room_alias:hs1"

			setRoomAliasResp(t, alice, roomID, roomAlias)

			res = listRoomAliasesResp(t, alice, roomID)

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyEqual("aliases", []interface{}{roomAlias}),
				},
			})
		})
	})
}
