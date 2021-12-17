package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/matrix-org/complement/runtime"
)

// @shadowjonathan: do we need this test anymore?
// sytest: Getting push rules doesn't corrupt the cache SYN-390
func TestPushRuleCacheHealth(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // Dendrite does not support push notifications (yet)

	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "r0", "pushrules", "global", "sender", alice.UserID}, client.WithJSONBody(t, map[string]interface{}{
		"actions": []string{"dont_notify"},
	}))

	// the extra "" is to make sure the submitted URL ends with a trailing slash
	res := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "pushrules", ""})

	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyEqual("global.sender.0.actions.0", "dont_notify"),
		},
	})

	res = alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "pushrules", ""})

	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyEqual("global.sender.0.actions.0", "dont_notify"),
		},
	})
}
