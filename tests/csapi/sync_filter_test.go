package csapi_tests

import (
	"io/ioutil"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestSyncFilter(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	authedClient := deployment.Client(t, "hs1", "@alice:hs1")
	// sytest: Can create filter
	t.Run("Can create filter", func(t *testing.T) {
		createFilter(t, authedClient, map[string]interface{}{
			"room": map[string]interface{}{
				"timeline": map[string]int{
					"limit": 10,
				},
			},
		})
	})
	// sytest: Can download filter
	t.Run("Can download filter", func(t *testing.T) {
		filterID := createFilter(t, authedClient, map[string]interface{}{
			"room": map[string]interface{}{
				"timeline": map[string]int{
					"limit": 10,
				},
			},
		})
		res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "user", "@alice:hs1", "filter", filterID})
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyPresent("room"),
				match.JSONKeyEqual("room.timeline.limit", float64(10)),
			},
		})

	})
}

func createFilter(t *testing.T, c *client.CSAPI, filterContent map[string]interface{}) string {
	t.Helper()
	res := c.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "user", c.UserID, "filter"}, client.WithJSONBody(t, filterContent))
	if res.StatusCode != 200 {
		t.Fatalf("MatchResponse got status %d want 200", res.StatusCode)
	}
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("unable to read response body: %v", err)
	}

	filterID := gjson.GetBytes(body, "filter_id").Str

	return filterID

}
