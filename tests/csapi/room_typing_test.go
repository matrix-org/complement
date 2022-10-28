package csapi_tests

import (
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

	bob.MustSyncUntil(t, client.SyncReq{Since: token}, client.SyncEphemeralHas(roomID, func(result gjson.Result) bool {
		if result.Get("type").Str != "m.typing" {
			return false
		}
		for _, item := range result.Get("content").Get("user_ids").Array() {
			if item.Str == alice.UserID {
				return true
			}
		}
		return false
	}))
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

		sawAlice := false

		// Go through all users, return early if typing user is not alice
		for _, item := range result.Get("content.user_ids").Array() {
			if item.Str == alice.UserID {
				sawAlice = true
			} else {
				t.Errorf("Saw one additional user typing, expected only alice: %s", item.Str)
				return false
			}
		}

		return sawAlice
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
