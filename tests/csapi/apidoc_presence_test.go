// +build !dendrite_blacklist

// Rationale for being included in Dendrite's blacklist: https://github.com/matrix-org/complement/pull/104#discussion_r617646624

package csapi_tests

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestPresence(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	// sytest: GET /presence/:user_id/status fetches initial status
	t.Run("GET /presence/:user_id/status fetches initial status", func(t *testing.T) {
		res := alice.DoFunc(t, "GET", []string{"_matrix", "client", "v3", "presence", "@alice:hs1", "status"})
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyPresent("presence"),
			},
		})
	})
	// sytest: PUT /presence/:user_id/status updates my presence
	t.Run("PUT /presence/:user_id/status updates my presence", func(t *testing.T) {
		statusMsg := "Testing something"
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"status_msg": statusMsg,
			"presence":   "online",
		})
		res := alice.DoFunc(t, "PUT", []string{"_matrix", "client", "v3", "presence", "@alice:hs1", "status"}, reqBody)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
		})
		res = alice.DoFunc(t, "GET", []string{"_matrix", "client", "v3", "presence", "@alice:hs1", "status"})
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyPresent("presence"),
				match.JSONKeyEqual("status_msg", statusMsg),
			},
		})
	})
	// sytest: Presence can be set from sync
	t.Run("Presence can be set from sync", func(t *testing.T) {
		_, bobSinceToken := bob.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

		alice.MustSync(t, client.SyncReq{TimeoutMillis: "0", SetPresence: "unavailable"})

		bob.MustSyncUntil(t, client.SyncReq{Since: bobSinceToken}, client.SyncPresenceHas(alice.UserID, b.Ptr("unavailable")))
	})
	// sytest: Presence changes are reported to local room members
	t.Run("Presence changes are reported to local room members", func(t *testing.T) {
		_, bobSinceToken := bob.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

		statusMsg := "Update for room members"
		alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "presence", "@alice:hs1", "status"},
			client.WithJSONBody(t, map[string]interface{}{
				"status_msg": statusMsg,
				"presence":   "online",
			}),
		)

		bob.MustSyncUntil(t, client.SyncReq{Since: bobSinceToken},
			client.SyncPresenceHas(alice.UserID, b.Ptr("online"), func(ev gjson.Result) bool {
				return ev.Get("content.status_msg").Str == statusMsg
			}),
		)
	})
	// sytest: Presence changes to UNAVAILABLE are reported to local room members
	t.Run("Presence changes to UNAVAILABLE are reported to local room members", func(t *testing.T) {
		_, bobSinceToken := bob.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

		alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "presence", "@alice:hs1", "status"},
			client.WithJSONBody(t, map[string]interface{}{
				"presence": "unavailable",
			}),
		)

		bob.MustSyncUntil(t, client.SyncReq{Since: bobSinceToken},
			client.SyncPresenceHas(alice.UserID, b.Ptr("unavailable")),
		)
	})
}
