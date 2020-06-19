package tests

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/gomatrixserverlib"
)

// Tests that the server is capable of making outbound /send requests
func TestOutboundFederationSend(t *testing.T) {
	deployment := MustDeploy(t, "federation_send", b.BlueprintOneToOneRoom.Name)
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

	charlie := srv.UserID("charlie")
	serverRoom := srv.MustMakeRoom(t, gomatrixserverlib.RoomVersionV5, federation.InitialRoomEvents(gomatrixserverlib.RoomVersionV5, charlie))
	roomAlias := srv.Alias(serverRoom.RoomID, "flibble")

	// alice := b.BlueprintOneToOneRoom.Homeservers[0].Users[0]
	// join the room
	joinURL := deployment.HS["hs1"].BaseURL + "/_matrix/client/r0/join/" + url.PathEscape(roomAlias) + "?access_token=" + url.QueryEscape(deployment.HS["hs1"].AccessTokens["@alice:hs1"])
	res, err := http.Post(joinURL, "application/json", strings.NewReader(`{}`))
	MustNotError(t, "POST returned error", err)
	MustHaveStatus(t, res, 200) // TODO: 200

	// send a message

}
