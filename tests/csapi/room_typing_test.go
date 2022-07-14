package csapi_tests

import (
	"fmt"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
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

	bob.MustSyncUntil(t, client.SyncReq{Since: token}, func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		key := "rooms.join." + client.GjsonEscape(roomID) + ".ephemeral.events"
		array := topLevelSyncJSON.Get(key)
		if !array.Exists() {
			return fmt.Errorf("Key %s does not exist", key)
		}
		if !array.IsArray() {
			return fmt.Errorf("Key %s exists but it isn't an array", key)
		}
		goArray := array.Array()
		for _, ev := range goArray {
			if ev.Get("type").Str != "m.typing" {
				continue
			}
			for _, item := range ev.Get("content").Get("user_ids").Array() {
				if item.Str == alice.UserID {
					return nil
				}
			}
		}
		return fmt.Errorf("no typing events")
	})
}
