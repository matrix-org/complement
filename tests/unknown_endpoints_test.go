package tests

import (
	"net/http"
	"testing"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func queryUnknownEndpoint(t *testing.T, user *client.CSAPI, paths []string) {
	t.Helper()

	res := user.Do(t, "GET", paths)
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusNotFound,
		JSON: []match.JSON{
			match.JSONKeyEqual("errcode", "M_UNRECOGNIZED"),
		},
	})
}

func queryUnknownMethod(t *testing.T, user *client.CSAPI, method string, paths []string) {
	t.Helper()

	res := user.Do(t, method, paths)
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusMethodNotAllowed,
		JSON: []match.JSON{
			match.JSONKeyEqual("errcode", "M_UNRECOGNIZED"),
		},
	})
}

// Homeservers should return a 404 for unknown endpoints and 405 for incorrect
// methods to known endpoints.
func TestUnknownEndpoints(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	// A completely unknown prefix to the matrix project.
	t.Run("Unknown prefix", func(t *testing.T) {
		queryUnknownEndpoint(t, alice, []string{"_matrix", "unknown"})
	})

	// Unknown client-server endpoints.
	t.Run("Client-server endpoints", func(t *testing.T) {
		queryUnknownEndpoint(t, alice, []string{"_matrix", "client", "unknown"})
		// v1 should exist, but not v1/unknown.
		queryUnknownEndpoint(t, alice, []string{"_matrix", "client", "v1", "unknown"})
		queryUnknownEndpoint(t, alice, []string{"_matrix", "client", "v3", "room", "unknown"})

		queryUnknownMethod(t, alice, "PUT", []string{"_matrix", "client", "v3", "login"})
	})

	// Unknown server-server endpoints.
	t.Run("Server-server endpoints", func(t *testing.T) {
		queryUnknownEndpoint(t, alice, []string{"_matrix", "federation", "unknown"})
		// v1 should exist, but not v1/unknown.
		queryUnknownEndpoint(t, alice, []string{"_matrix", "federation", "v1", "unknown"})

		queryUnknownMethod(t, alice, "PUT", []string{"_matrix", "federation", "v1", "version"})
	})

	// Unknown key endpoints (part of the Server-server API under a different prefix).
	t.Run("Key endpoints", func(t *testing.T) {
		queryUnknownEndpoint(t, alice, []string{"_matrix", "key", "unknown"})
		// v3 should exist, but not v3/unknown.
		queryUnknownEndpoint(t, alice, []string{"_matrix", "key", "v2", "unknown"})

		queryUnknownMethod(t, alice, "PUT", []string{"_matrix", "key", "v2", "query"})
	})

	// Unknown media endpoints.
	t.Run("Media endpoints", func(t *testing.T) {
		queryUnknownEndpoint(t, alice, []string{"_matrix", "media", "unknown"})
		// v3 should exist, but not v3/unknown.
		queryUnknownEndpoint(t, alice, []string{"_matrix", "media", "v3", "unknown"})

		queryUnknownMethod(t, alice, "PATCH", []string{"_matrix", "media", "v3", "upload"})
	})
}
