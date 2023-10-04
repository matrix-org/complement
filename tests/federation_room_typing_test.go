package tests

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/b"
)

func awaitTyping(userId string) func(result gjson.Result) bool {
	return func(result gjson.Result) bool {
		if result.Get("type").Str != "m.typing" {
			return false
		}

		for _, item := range result.Get("content").Get("user_ids").Array() {
			if item.Str == userId {
				return true
			}
		}

		return false
	}
}

// sytest: Typing notifications also sent to remote room members
func TestRemoteTyping(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationTwoLocalOneRemote)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")
	charlie := deployment.Client(t, "hs2", "@charlie:hs2")

	roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.JoinRoom(t, roomID, nil)
	charlie.JoinRoom(t, roomID, []string{"hs1"})

	bobToken := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
	charlieToken := charlie.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(charlie.UserID, roomID))

	alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "typing", alice.UserID}, client.WithJSONBody(t, map[string]interface{}{
		"typing":  true,
		"timeout": 10000,
	}))

	bob.MustSyncUntil(t, client.SyncReq{Since: bobToken}, client.SyncEphemeralHas(roomID, awaitTyping(alice.UserID)))

	charlie.MustSyncUntil(t, client.SyncReq{Since: charlieToken}, client.SyncEphemeralHas(roomID, awaitTyping(alice.UserID)))
}
