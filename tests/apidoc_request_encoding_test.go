package tests

import (
	"testing"

	"encoding/json"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestRequestEncodingFails(t *testing.T) {
	deployment := Deploy(t, "request_encoding", b.BlueprintAlice)
	defer deployment.Destroy(t)
	unauthedClient := deployment.Client(t, "hs1", "")
	testString := `{ "test":"a` + "\x81" + `" }`
	// sytest: POST rejects invalid utf-8 in JSON
	t.Run("POST rejects invalid utf-8 in JSON", func(t *testing.T) {
		res, err := unauthedClient.Do(t, "POST", []string{"_matrix", "client", "r0", "register"}, json.RawMessage(testString), nil)
		if err != nil {
			t.Fatalf("failed to make request to register %v", err)
		}
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 400,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_NOT_JSON"),
			},
		})
	})
}
