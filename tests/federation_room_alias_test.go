package tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

// sytest: Remote room alias queries can handle Unicode
func TestRemoteAliasRequestsUnderstandUnicode(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs2", "@bob:hs2")

	const unicodeAlias = "#老虎Â£я🤨👉ඞ:hs1"

	roomID := alice.CreateRoom(t, map[string]interface{}{})

	alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "directory", "room", unicodeAlias}, client.WithJSONBody(t, map[string]interface{}{
		"room_id": roomID,
	}))

	res := bob.DoFunc(t, "GET", []string{"_matrix", "client", "v3", "directory", "room", unicodeAlias})
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 200,
		JSON: []match.JSON{
			match.JSONKeyEqual("room_id", roomID),
		},
	})
}
