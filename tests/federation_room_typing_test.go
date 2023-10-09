package tests

import (
	"testing"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
)

// sytest: Typing notifications also sent to remote room members
func TestRemoteTyping(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationTwoLocalOneRemote)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")
	charlie := deployment.Client(t, "hs2", "@charlie:hs2")

	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.MustJoinRoom(t, roomID, nil)
	charlie.MustJoinRoom(t, roomID, []string{"hs1"})

	bobToken := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
	charlieToken := charlie.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(charlie.UserID, roomID))

	alice.SendTyping(t, roomID, true, 10000)

	bob.MustSyncUntil(t, client.SyncReq{Since: bobToken}, client.SyncUsersTyping(roomID, []string{alice.UserID}))

	charlie.MustSyncUntil(t, client.SyncReq{Since: charlieToken}, client.SyncUsersTyping(roomID, []string{alice.UserID}))
}
