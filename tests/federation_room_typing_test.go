package tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
)

// sytest: Typing notifications also sent to remote room members
func TestRemoteTyping(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	charlie := deployment.Register(t, "hs2", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.MustJoinRoom(t, roomID, nil)
	charlie.MustJoinRoom(t, roomID, []string{"hs1"})

	bobToken := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
	charlieToken := charlie.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(charlie.UserID, roomID))

	alice.SendTyping(t, roomID, true, 10000)

	bob.MustSyncUntil(t, client.SyncReq{Since: bobToken}, client.SyncUsersTyping(roomID, []string{alice.UserID}))

	charlie.MustSyncUntil(t, client.SyncReq{Since: charlieToken}, client.SyncUsersTyping(roomID, []string{alice.UserID}))
}
