package csapi_tests

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
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

// sytest: Typing notifications don't leak
func TestLeakyTyping(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")
	charlie := deployment.RegisterUser(t, "hs1", "charlie", "charliepassword", false)

	// Alice creates a room. Bob joins it.
	roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.JoinRoom(t, roomID, nil)

	bobToken := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	_, charlieToken := charlie.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

	// Alice types in that room. Bob should see her typing.
	alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "typing", alice.UserID}, client.WithJSONBody(t, map[string]interface{}{
		"typing":  true,
		"timeout": 10000,
	}))

	bob.MustSyncUntil(t, client.SyncReq{Since: bobToken}, client.SyncEphemeralHas(roomID, func(result gjson.Result) bool {
		if result.Get("type").Str != "m.typing" {
			return false
		}

		err := match.JSONCheckOff("content.user_ids", []interface{}{
			alice.UserID,
		}, func(result gjson.Result) interface{} {
			return result.Str
		}, nil)([]byte(result.Raw))

		return err == nil
	}))

	// Charlie is not in the room, so should not see Alice typing.
	res, _ := charlie.MustSync(t, client.SyncReq{TimeoutMillis: "2000", Since: charlieToken})

	err := client.SyncEphemeralHas(roomID, func(result gjson.Result) bool {
		if result.Get("type").Str != "m.typing" {
			return false
		}
		for _, item := range result.Get("content").Get("user_ids").Array() {
			if item.Str == charlie.UserID {
				return true
			}
		}
		return false
	})(charlie.UserID, res)

	if err == nil {
		t.Fatalf("Received unexpected typing notification: %s", res.Raw)
	}
}
