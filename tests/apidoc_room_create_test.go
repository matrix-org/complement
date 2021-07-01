package tests

import (
	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
	"testing"
)

func TestRoomCreate(t *testing.T){
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	authedClient := deployment.Client(t, "hs1", "@alice:hs1")
	t.Run("Parallel", func(t *testing.T){
		// Sytest: POST /createRoom makes a public room
		t.Run("POST /createRoom makes a public room", func(t *testing.T) {
			t.Parallel()
			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"visibility": "public",
				"room_alias_name": "30-room-create-alias-random",
			})
			res := authedClient.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "createRoom"}, reqBody)
			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("room_id"),
					match.JSONKeyPresent("room_alias"),
					match.JSONKeyTypeEqual("room_id", gjson.String),
				},
			})
		})
		// Sytest: POST /createRoom makes a private room
		t.Run("POST /createRoom makes a private room", func(t *testing.T) {
			t.Parallel()
			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"visibility": "private",
			})
			res := authedClient.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "createRoom"}, reqBody)
			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("room_id"),
					match.JSONKeyTypeEqual("room_id", gjson.String),
				},
			})
		})
	})
}
