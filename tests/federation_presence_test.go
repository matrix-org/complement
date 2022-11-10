package tests

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
)

func TestRemotePresence(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs2", "@bob:hs2")

	// sytest: Presence changes are also reported to remote room members
	t.Run("Presence changes are also reported to remote room members", func(t *testing.T) {
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
	// sytest: Presence changes to UNAVAILABLE are reported to remote room members
	t.Run("Presence changes to UNAVAILABLE are reported to remote room members", func(t *testing.T) {
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
