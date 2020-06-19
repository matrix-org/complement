package tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/gomatrixserverlib"
)

// Tests that the server is capable of making outbound /send requests
func TestOutboundFederationSend(t *testing.T) {
	deployment := MustDeploy(t, "federation_send", b.BlueprintAlice.Name)
	defer func() {
		deployment.Destroy(t.Failed())
	}()

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleDirectoryLookups(),
	)
	cancel := srv.Listen()
	defer cancel()

	ver := gomatrixserverlib.RoomVersionV5
	charlie := srv.UserID("charlie")
	serverRoom := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, charlie))
	roomAlias := srv.Alias(serverRoom.RoomID, "flibble")

	// join the room
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	alice.MustDo(t, "POST", []string{"_matrix", "client", "r0", "join", roomAlias}, struct{}{})

}
