package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

// Endpoint: https://matrix.org/docs/spec/client_server/r0.6.1#post-matrix-client-r0-keys-query
// This test asserts that the server is correctly rejecting input that does not match the request format given.
// Specifically, it replaces [$device_id] with { $device_id: bool } which, if not type checked, will be processed
// like an array in Python and hence go un-noticed. In Go however it will result in a 400. The correct behaviour is
// to return a 400. Element iOS uses this erroneous format.
func TestKeysQueryWithDeviceIDAsObjectFails(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	res := alice.Do(t, "POST", []string{"_matrix", "client", "v3", "keys", "query"},
		client.WithJSONBody(t, map[string]interface{}{
			"device_keys": map[string]interface{}{
				bob.UserID: map[string]bool{
					"device_id1": true,
					"device_id2": true,
				},
			},
		}),
	)
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 400,
		JSON: []match.JSON{
			match.JSONKeyEqual("errcode", "M_BAD_JSON"),
		},
	})
}
