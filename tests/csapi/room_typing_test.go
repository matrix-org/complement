package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
)

// sytest: PUT /rooms/:room_id/typing/:user_id sets typing notification
func TestTyping(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})

	bob.MustJoinRoom(t, roomID, nil)

	token := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
	alice.SendTyping(t, roomID, true, 10000)

	// sytest: Typing notification sent to local room members
	t.Run("Typing notification sent to local room members", func(t *testing.T) {
		bob.MustSyncUntil(t, client.SyncReq{Since: token}, client.SyncUsersTyping(roomID, []string{alice.UserID}))
	})

	// sytest: Typing can be explicitly stopped
	t.Run("Typing can be explicitly stopped", func(t *testing.T) {
		alice.SendTyping(t, roomID, false, 0)
		bob.MustSyncUntil(t, client.SyncReq{Since: token}, client.SyncUsersTyping(roomID, []string{}))
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
	bob.MustJoinRoom(t, roomID, nil)

	bobToken := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	_, charlieToken := charlie.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

	// Alice types in that room. Bob should see her typing.
	alice.SendTyping(t, roomID, true, 10000)

	bob.MustSyncUntil(t, client.SyncReq{Since: bobToken}, client.SyncUsersTyping(roomID, []string{alice.UserID}))

	// Charlie is not in the room, so should not see Alice typing, or anything from that room at all.
	res, _ := charlie.MustSync(t, client.SyncReq{TimeoutMillis: "1000", Since: charlieToken})
	if res.Get("rooms.join." + client.GjsonEscape(roomID)).Exists() {
		t.Fatalf("Received unexpected room: %s", res.Raw)
	}
}
