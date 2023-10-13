package csapi_tests

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

// https://spec.matrix.org/v1.1/#specification-versions
// altered to limit to X = 1+, as v0 has never existed
const GlobalVersionRegex = `v[1-9]\d*\.\d+(?:-\S+)?`

// https://github.com/matrix-org/matrix-doc/blob/client_server/r0.6.1/specification/index.rst#specification-versions
// altered to limit to X = 0 (r0), as r1+ will never exist.
const r0Regex = `r0\.\d+\.\d+`

func TestVersionStructure(t *testing.T) {
	deployment := complement.Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	client := deployment.UnauthenticatedClient(t, "hs1")

	// sytest: Version responds 200 OK with valid structure
	t.Run("Version responds 200 OK with valid structure", func(t *testing.T) {
		res := client.MustDo(t, "GET", []string{"_matrix", "client", "versions"})

		// Matches;
		// - r0.?.?
		//  where ? is any single digit
		// - v1^.*(-#)
		//  where 1^ is 1 through 9 for the first digit, then any digit thereafter,
		//  and * is any single or multiple of digits
		//  optionally with dash-separated metadata: (-#)
		versionRegex, _ := regexp.Compile("^(" + r0Regex + "|" + GlobalVersionRegex + ")$")

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyPresent("versions"),
				match.JSONArrayEach("versions", func(val gjson.Result) error {
					if val.Type != gjson.String {
						return fmt.Errorf("'versions' value is not a string: %s", val.Raw)
					}
					if !versionRegex.MatchString(val.Str) {
						return fmt.Errorf("value in 'versions' array did not match version regex: %s", val.Str)
					}
					return nil
				}),
				// Check when unstable_features is present if it's an object
				func(body gjson.Result) error {
					res := body.Get("unstable_features")
					if !res.Exists() {
						return nil
					}
					if !res.IsObject() {
						return fmt.Errorf("unstable_features was present, and wasn't an object")
					}
					for k, v := range res.Map() {
						// gjson doesn't have a "boolean" type to check against
						if v.Type != gjson.True && v.Type != gjson.False {
							return fmt.Errorf("value for key 'unstable_features.%s' is of the wrong type, got %s want boolean", k, v.Type)
						}
					}
					return nil
				},
			},
		})
	})
}
