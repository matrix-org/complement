package csapi_tests

import (
	"testing"

	"encoding/json"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestRequestEncodingFails(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	unauthedClient := deployment.Client(t, "hs1", "")
	testString := `{ "test":"a` + "\x81" + `" }`
	// sytest: POST rejects invalid utf-8 in JSON
	t.Run("POST rejects invalid utf-8 in JSON", func(t *testing.T) {
		res := unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "v3", "register"}, client.WithRawBody(json.RawMessage(testString)))
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 400,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_NOT_JSON"),
			},
		})
	})
}
