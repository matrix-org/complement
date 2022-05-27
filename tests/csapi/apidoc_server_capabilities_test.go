package csapi_tests

import (
	"net/http"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
)

func TestServerCapabilities(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	unauthedClient := deployment.Client(t, "hs1", "")
	authedClient := deployment.Client(t, "hs1", "@alice:hs1")

	// sytest: GET /capabilities is present and well formed for registered user
	data := authedClient.GetCapabilities(t)
	j := gjson.ParseBytes(data)
	if !j.Get(`capabilities.m\.room_versions`).Exists() {
		t.Fatal("expected m.room_versions not found")
	}
	if !j.Get(`capabilities.m\.change_password`).Exists() {
		t.Fatal("expected m.change_password not found")
	}

	// sytest: GET /v3/capabilities is not public
	res := unauthedClient.DoFunc(t, "GET", []string{"_matrix", "client", "v3", "capabilities"})
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusUnauthorized,
	})
}
