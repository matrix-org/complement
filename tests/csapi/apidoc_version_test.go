package csapi_tests

import (
	"fmt"
	"regexp"
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

		// Matches;
		// - r0.?.?
		//  where ? is any single digit
		// - v1^.*
		//  where 1^ is 1 through 9 for the first digit, then any digit thereafter,
		//  and * is any single or multiple of digits
		versionRegex, _ := regexp.Compile(`^(v[1-9]\d*\.\d+|r0\.\d\.\d)$`)

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyPresent("versions"),
				match.JSONArrayEach("versions", func(val gjson.Result) error {
					if val.Type != gjson.String {
						return fmt.Errorf("'versions' value is not a string: %s", val.Raw)
					}
					if !versionRegex.MatchString(val.Str) {
						return fmt.Errorf("'versions' value did not match version regex: %s", val.Str)
					}
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
