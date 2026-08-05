package tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
)

// sytest: Remote room alias queries can handle Unicode
func TestRemoteAliasRequestsUnderstandUnicode(t *testing.T) {
	// Venator: does not yet implement federation
	runtime.SkipIf(t, runtime.Venator)
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})

	const unicodeAlias = "#老虎Â£я🤨👉ඞ:hs1"

	roomID := alice.MustCreateRoom(t, map[string]interface{}{})

	alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "directory", "room", unicodeAlias}, client.WithJSONBody(t, map[string]interface{}{
		"room_id": roomID,
	}))

	res := bob.Do(t, "GET", []string{"_matrix", "client", "v3", "directory", "room", unicodeAlias})
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 200,
		JSON: []match.JSON{
			match.JSONKeyEqual("room_id", roomID),
		},
	})
}
