package csapi_tests

import (
	"net/http"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

func TestPublicRooms(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	t.Run("parallel", func(t *testing.T) {
		// sytest: Can search public room list
		t.Run("Can search public room list", func(t *testing.T) {
			t.Parallel()
			authedClient := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

			roomID := authedClient.MustCreateRoom(t, map[string]any{
				"visibility": "public",
				"name":       "Test Name",
				"topic":      "Test Topic Wombles",
			})

			authedClient.MustDo(
				t,
				"POST",
				[]string{"_matrix", "client", "v3", "publicRooms"},
				client.WithJSONBody(t, map[string]any{
					"filter": map[string]any{
						"generic_search_term": "wombles",
					},
				}),
				client.WithRetryUntil(15*time.Second, func(res *http.Response) bool {
					body := must.ParseJSON(t, res.Body)

					must.MatchGJSON(
						t,
						body,
						match.JSONKeyPresent("chunk"),
						match.JSONKeyTypeEqual("chunk", gjson.JSON),
					)

					chunk := body.Get("chunk")
					if !chunk.IsArray() {
						t.Logf("chunk is not an array")
						return false
					}

					results := chunk.Array()
					if len(results) != 1 {
						t.Logf("expected a single search result, got %d", len(results))
						return false
					}

					foundRoomID := results[0].Get("room_id").Str
					if foundRoomID != roomID {
						t.Logf("expected room_id %s in search results, got %s", roomID, foundRoomID)
						return false
					}

					return true
				}),
			)
		})
	})
}
