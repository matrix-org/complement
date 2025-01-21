package csapi_tests

import (
	"testing"

	"encoding/json"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

func TestRequestEncodingFails(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	unauthedClient := deployment.UnauthenticatedClient(t, "hs1")
	testString := `{ "test":"a` + "\x81" + `" }`
	// sytest: POST rejects invalid utf-8 in JSON
	t.Run("POST rejects invalid utf-8 in JSON", func(t *testing.T) {
		res := unauthedClient.Do(t, "POST", []string{"_matrix", "client", "v3", "register"}, client.WithRawBody(json.RawMessage(testString)))
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 400,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_NOT_JSON"),
			},
		})
	})
}
