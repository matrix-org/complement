package csapi_tests

import (
	"fmt"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestVersionStructure(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	client := deployment.Client(t, "hs1", "")

	// sytest: Version responds 200 OK with valid structure
	t.Run("Version responds 200 OK with valid structure", func(t *testing.T) {
		res := client.MustDoFunc(t, "GET", []string{"_matrix", "client", "versions"})

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyPresent("versions"),
				match.JSONArrayEach("versions", func(val gjson.Result) error {
					if val.Type != gjson.String {
						return fmt.Errorf("'versions' value is not a string: %s", val.Raw)
					}
					// versions format is currently relatively undefined, see https://github.com/matrix-org/matrix-doc/issues/3594
					return nil
				}),
				// Check when unstable_features is present if it's an object
				func(body []byte) error {
					res := gjson.GetBytes(body, "unstable_features")
					if !res.Exists() {
						return nil
					}
					if !res.IsObject() {
						return fmt.Errorf("unstable_features was present, and wasn't an object")
					}
					return nil
				},
			},
		})
	})
}
