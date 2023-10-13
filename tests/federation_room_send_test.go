package tests

import (
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/gomatrixserverlib"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/internal/federation"
)

// TODO:
// Inbound federation can receive events
// Inbound federation can receive redacted events
// Ephemeral messages received from servers are correctly expired
// Events whose auth_events are in the wrong room do not mess up the room state

// Tests that the server is capable of making outbound /send requests
func TestOutboundFederationSend(t *testing.T) {
	deployment := complement.Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	waiter := helpers.NewWaiter()
	wantEventType := "m.room.message"

	// create a remote homeserver
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(
			// listen for PDU events in transactions
			func(ev gomatrixserverlib.PDU) {
				defer waiter.Finish()

				if ev.Type() != wantEventType {
					t.Errorf("Wrong event type, got %s want %s", ev.Type(), wantEventType)
				}
			},
			nil,
		),
	)
	cancel := srv.Listen()
	defer cancel()

	// the remote homeserver creates a public room
	ver := alice.GetDefaultRoomVersion(t)
	charlie := srv.UserID("charlie")
	serverRoom := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, charlie))
	roomAlias := srv.MakeAliasMapping("flibble", serverRoom.RoomID)

	// the local homeserver joins the room
	alice.MustJoinRoom(t, roomAlias, []string{deployment.GetConfig().HostnameRunningComplement})

	// the local homeserver sends an event into the room
	alice.SendEventSynced(t, serverRoom.RoomID, b.Event{
		Type: wantEventType,
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world!",
		},
	})

	// the remote homeserver then waits for the desired event to appear in a transaction
	waiter.Wait(t, 5*time.Second)
}
