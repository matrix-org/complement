package csapi_tests

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
)

func TestLogout(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	password := "superuser"
	testuser := deployment.RegisterUser(t, "hs1", "testuser", password, false)

	// sytest: Can logout current device
	t.Run("Can logout current device", func(t *testing.T) {
		deviceID, c := createSession(t, deployment, testuser.UserID, password)
		res := c.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "devices"})
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("devices", 2),
			},
		})
		res = c.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "logout"})
		must.MatchResponse(t, res, match.HTTPResponse{StatusCode: http.StatusOK})
		// the session should be invalidated
		res = c.DoFunc(t, "GET", []string{"_matrix", "client", "v3", "sync"})
		must.MatchResponse(t, res, match.HTTPResponse{StatusCode: http.StatusUnauthorized})
		// verify with first device
		res = testuser.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "devices"})
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("devices", 1),
				match.JSONArrayEach("devices", func(result gjson.Result) error {
					if result.Get("device_id").Str == deviceID {
						return fmt.Errorf("second device still exists")
					}
					return nil
				}),
			},
		})
		// initial session should still be able to sync
		testuser.MustSync(t, client.SyncReq{})
	})
	// sytest: Can logout all devices
	t.Run("Can logout all devices", func(t *testing.T) {
		_, c := createSession(t, deployment, testuser.UserID, password)
		res := c.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "devices"})
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("devices", 2),
			},
		})
		res = c.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "logout", "all"})
		must.MatchResponse(t, res, match.HTTPResponse{StatusCode: http.StatusOK})
		// all sessions should be invalidated
		res = c.DoFunc(t, "GET", []string{"_matrix", "client", "v3", "sync"})
		must.MatchResponse(t, res, match.HTTPResponse{StatusCode: http.StatusUnauthorized})
		res = testuser.DoFunc(t, "GET", []string{"_matrix", "client", "v3", "sync"})
		must.MatchResponse(t, res, match.HTTPResponse{StatusCode: http.StatusUnauthorized})

	})
	// sytest: Request to logout with invalid an access token is rejected
	t.Run("Request to logout with invalid an access token is rejected", func(t *testing.T) {
		_, c := createSession(t, deployment, testuser.UserID, password)
		c.AccessToken = "invalidAccessToken"
		res := c.DoFunc(t, "POST", []string{"_matrix", "client", "v3", "logout"})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: http.StatusUnauthorized,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_UNKNOWN_TOKEN"),
			},
		})
	})
	// sytest: Request to logout without an access token is rejected
	t.Run("Request to logout without an access token is rejected", func(t *testing.T) {
		_, c := createSession(t, deployment, testuser.UserID, password)
		c.AccessToken = ""
		res := c.DoFunc(t, "POST", []string{"_matrix", "client", "v3", "logout"})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: http.StatusUnauthorized,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_MISSING_TOKEN"),
			},
		})
	})
}
