//go:build !dendrite_blacklist
// +build !dendrite_blacklist

// Rationale for being included in Dendrite's blacklist: https://github.com/matrix-org/complement/pull/104#discussion_r617646624

package csapi_tests

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

func TestPresence(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	// to share presence alice and bob must be in a shared room
	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.MustJoinRoom(t, roomID, []string{"hs1"})

	// sytest: GET /presence/:user_id/status fetches initial status
	t.Run("GET /presence/:user_id/status fetches initial status", func(t *testing.T) {
		res := alice.Do(t, "GET", []string{"_matrix", "client", "v3", "presence", alice.UserID, "status"})
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
		res := alice.Do(t, "PUT", []string{"_matrix", "client", "v3", "presence", alice.UserID, "status"}, reqBody)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 200,
		})
		res = alice.Do(t, "GET", []string{"_matrix", "client", "v3", "presence", alice.UserID, "status"})
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
		alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "presence", alice.UserID, "status"},
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

		alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "presence", alice.UserID, "status"},
			client.WithJSONBody(t, map[string]interface{}{
				"presence": "unavailable",
			}),
		)

		bob.MustSyncUntil(t, client.SyncReq{Since: bobSinceToken},
			client.SyncPresenceHas(alice.UserID, b.Ptr("unavailable")),
		)
	})
}
