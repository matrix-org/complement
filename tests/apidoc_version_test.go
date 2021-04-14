package tests

import (
	"fmt"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestVersionStructure(t *testing.T) {
	deployment := Deploy(t, "test_version", b.BlueprintAlice)
	defer deployment.Destroy(t)
	unauthedClient := deployment.Client(t, "hs1", "")
	// sytest: Version responds 200 OK with valid structure
	t.Run("Version responds 200 OK with valid structure", func(t *testing.T) {
		res := unauthedClient.MustDo(t, "GET", []string{"_matrix", "client", "versions"}, nil)

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyPresent("versions"),
				match.JSONArrayEach("versions", func(val gjson.Result) error {
					if val.Type != gjson.String {
						return fmt.Errorf("'versions' value is not a string: %s", val.Raw)
					}
					return nil
				}),
			},
		})
	})
}
