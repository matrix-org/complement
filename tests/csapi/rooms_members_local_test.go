package csapi_tests

import (
	"fmt"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/runtime"
	"github.com/tidwall/gjson"
)

func TestMembersLocal(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")
	roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})

	t.Run("Parallel", func(t *testing.T) {
		// sytest: New room members see their own join event
		t.Run("New room members see their own join event", func(t *testing.T) {
			t.Parallel()
			// SyncJoinedTo already checks everything we need to know
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
		})

		// sytest: Existing members see new members' join events
		t.Run("Existing members see new members' join events", func(t *testing.T) {
			t.Parallel()
			bob.JoinRoom(t, roomID, []string{})
			// SyncJoinedTo already checks everything we need to know
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
		})

		// sytest: Existing members see new members' presence
		t.Run("Existing members see new members' presence", func(t *testing.T) {
			runtime.SkipIf(t, runtime.Dendrite) // Still failing
			t.Parallel()
			alice.MustSyncUntil(t, client.SyncReq{},
				client.SyncJoinedTo(bob.UserID, roomID),
				func(clientUserID string, topLevelSyncJSON gjson.Result) error {
					presenceEvents := topLevelSyncJSON.Get("presence.events")
					if !presenceEvents.Exists() {
						return fmt.Errorf("presence.events does not exist")
					}
					for _, x := range presenceEvents.Array() {
						fieldsExists := x.Get("type").Exists() && x.Get("content").Exists() && x.Get("sender").Exists()
						if !fieldsExists {
							return fmt.Errorf("expected fields type, content and sender")
						}
						if x.Get("sender").Str == bob.UserID {
							return nil
						}
					}
					return fmt.Errorf("did not find %s in presence events", bob.UserID)
				},
			)
		})
	})

}
