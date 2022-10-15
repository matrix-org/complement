package csapi_tests

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/must"
)

// sytest: PUT /rooms/:room_id/typing/:user_id sets typing notification
func TestTyping(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})

	bob.JoinRoom(t, roomID, nil)

	token := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "typing", alice.UserID}, client.WithJSONBody(t, map[string]interface{}{
		"typing":  true,
		"timeout": 10000,
	}))

	// sytest: Typing notification sent to local room members
	t.Run("Typing notification sent to local room members", func(t *testing.T) {
		var usersSeenTyping []interface{}

		bob.MustSyncUntil(t, client.SyncReq{Since: token}, client.SyncEphemeralHas(roomID, func(result gjson.Result) bool {
			if result.Get("type").Str != "m.typing" {
				return false
			}

			var res = false

			for _, item := range result.Get("content").Get("user_ids").Array() {
				usersSeenTyping = append(usersSeenTyping, item.Str)

				if item.Str == alice.UserID {
					res = true
				}
			}

			return res
		}))

		must.CheckOffAll(t, usersSeenTyping, []interface{}{alice.UserID})
	})

	// sytest: Typing can be explicitly stopped
	t.Run("Typing can be explicitly stopped", func(t *testing.T) {
		alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "typing", alice.UserID}, client.WithJSONBody(t, map[string]interface{}{
			"typing": false,
		}))

		bob.MustSyncUntil(t, client.SyncReq{Since: token}, client.SyncEphemeralHas(roomID, func(result gjson.Result) bool {
			if result.Get("type").Str != "m.typing" {
				return false
			}

			return len(result.Get("content").Get("user_ids").Array()) == 0
		}))
	})
}
