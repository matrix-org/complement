package csapi_tests

import (
	"io/ioutil"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

func TestSyncFilter(t *testing.T) {
	deployment := complement.Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	authedClient := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
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
		res := authedClient.MustDo(t, "GET", []string{"_matrix", "client", "v3", "user", "@alice:hs1", "filter", filterID})
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
	res := c.MustDo(t, "POST", []string{"_matrix", "client", "v3", "user", c.UserID, "filter"}, client.WithJSONBody(t, filterContent))
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
