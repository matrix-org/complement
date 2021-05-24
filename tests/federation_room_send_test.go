package tests

import (
	"testing"
	"time"

	"github.com/matrix-org/gomatrixserverlib"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/complement/internal/federation"
)

// TODO:
// Inbound federation can receive events
// Inbound federation can receive redacted events
// Ephemeral messages received from servers are correctly expired
// Events whose auth_events are in the wrong room do not mess up the room state

// Tests that the server is capable of making outbound /send requests
func TestOutboundFederationSend(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	waiter := NewWaiter()
	wantEventType := "m.room.message"

	// create a remote homeserver
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(
			// listen for PDU events in transactions
			func(ev *gomatrixserverlib.Event) {
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
	ver := gomatrixserverlib.RoomVersionV5
	charlie := srv.UserID("charlie")
	serverRoom := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, charlie))
	roomAlias := srv.MakeAliasMapping("flibble", serverRoom.RoomID)

	// the local homeserver joins the room
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	alice.JoinRoom(t, roomAlias, []string{docker.HostnameRunningComplement})

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
