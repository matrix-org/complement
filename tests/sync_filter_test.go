package tests

import (
	"encoding/json"
	"io/ioutil"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/tidwall/gjson"
)

func TestSyncFilter(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	authedClient := deployment.Client(t, "hs1", "@alice:hs1")
	// sytest: Can create filter
	t.Run("Can create filter", func(t *testing.T) {
		reqBody, err := json.Marshal(map[string]interface{}{
			"room": map[string]interface{}{
				"timeline": map[string]int{
					"limit": 10,
				},
			},
		})
		if err != nil {
			t.Fatalf("failed to marshal JSON request body: %s", err)
		}
		create_filter(authedClient, reqBody, t, "@alice:hs1")
	})
}

func create_filter(authedClient *client.CSAPI, reqBody []byte, t *testing.T, userID string) string {
	res := authedClient.MustDo(t, "POST", []string{"_matrix", "client", "r0", "user", userID, "filter"}, json.RawMessage(reqBody))
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
