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
func xTestOutboundFederationSend(t *testing.T) {
	deployment := MustDeploy(t, "federation_send", b.BlueprintOneToOneRoom.Name)
	defer deployment.Destroy()

	srv := federation.NewServer(t,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
	)
	cancel := srv.Listen()
	defer cancel()

	charlie := srv.UserID("charlie")
	plContent := gomatrixserverlib.PowerLevelContent{}
	plContent.Defaults()
	serverRoom := srv.MustMakeRoom(t, gomatrixserverlib.RoomVersionV5, []b.Event{
		{
			Type:     "m.room.create",
			StateKey: b.Ptr(""),
			Sender:   charlie,
			Content:  map[string]interface{}{},
		},
		{
			Type:     "m.room.member",
			StateKey: b.Ptr(charlie),
			Sender:   charlie,
			Content: map[string]interface{}{
				"membership": "join",
			},
		},
		{
			Type:     "m.room.power_levels",
			StateKey: b.Ptr(""),
			Sender:   charlie,
			Content: map[string]interface{}{
				"state_default": plContent.StateDefault,
				"users_default": plContent.UsersDefault,
			},
		},
		{
			Type:     "m.room.join_rules",
			StateKey: b.Ptr(""),
			Sender:   charlie,
			Content: map[string]interface{}{
				"join_rule": "public",
			},
		},
	})
	roomAlias := srv.Alias(serverRoom.RoomID, "flibble")

	// alice := b.BlueprintOneToOneRoom.Homeservers[0].Users[0]
	// join the room
	joinURL := deployment.HS["hs1"].BaseURL + "/_matrix/client/r0/join/" + url.PathEscape(roomAlias)
	res, err := http.Post(joinURL, "application/json", strings.NewReader(`{}`))
	MustNotError(t, "POST returned error", err)
	MustHaveStatus(t, res, 200)

	// send a message

}
