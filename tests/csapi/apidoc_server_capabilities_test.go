package csapi_tests

import (
	"net/http"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

func TestServerCapabilities(t *testing.T) {
	deployment := complement.Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	unauthedClient := deployment.Client(t, "hs1", "")
	authedClient := deployment.Client(t, "hs1", "@alice:hs1")

	// sytest: GET /capabilities is present and well formed for registered user
	data := authedClient.GetCapabilities(t)

	must.MatchJSONBytes(
		t,
		data,
		match.JSONKeyPresent(`capabilities.m\.room_versions`),
		match.JSONKeyPresent(`capabilities.m\.change_password`),
	)

	// sytest: GET /v3/capabilities is not public
	res := unauthedClient.Do(t, "GET", []string{"_matrix", "client", "v3", "capabilities"})
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusUnauthorized,
	})
}
