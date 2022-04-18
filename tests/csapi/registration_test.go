package csapi_tests

import (
	"encoding/json"
	"io/ioutil"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
)

func Test11Register(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	unauthedClient := deployment.Client(t, "hs1", "")
	// sytest: registration accepts non-ascii passwords
	t.Run("Registration accepts non-ascii passwords", func(t *testing.T) {
		reqJson := json.RawMessage(`{"username": "test_user", "password": "übers3kr1t", "device_id": "xyzzy", "initial_device_display_name": "display_name"}`)
		resp := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "register"}, client.WithRawBody(reqJson))
		body, err := ioutil.ReadAll(resp.Body)
		session := gjson.GetBytes(body, "session")
		if err != nil {
			t.Fatalf("Failed to read response body: %s", err)
		}
		must.MatchResponse(t, resp, match.HTTPResponse{StatusCode: 401})
		auth := map[string]interface{}{
			"session": session.Str,
			"type":    "m.login.dummy",
		}
		reqBody := map[string]interface{}{
			"username":                    "test_user",
			"password":                    "übers3kr1t",
			"device_id":                   "xyzzy",
			"initial_device_display_name": "display_name",
			"auth":                        auth,
		}
		resp2 := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "register"}, client.WithJSONBody(t, reqBody))
		must.MatchResponse(t, resp2, match.HTTPResponse{JSON: []match.JSON{
			match.JSONKeyPresent("access_token"),
		}})
	})
}
