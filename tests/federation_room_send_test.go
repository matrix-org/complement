package tests

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/matrix-org/gomatrixserverlib"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/federation"
)

// TODO:
// Inbound federation can receive events
// Inbound federation can receive redacted events
// Ephemeral messages received from servers are correctly expired
// Events whose auth_events are in the wrong room do not mess up the room state

// Tests that the server is capable of making outbound /send requests
func TestOutboundFederationSend(t *testing.T) {
	deployment := Deploy(t, "federation_send", b.BlueprintAlice)
	defer deployment.Destroy(t)

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
	)
	cancel := srv.Listen()
	defer cancel()

	ver := gomatrixserverlib.RoomVersionV5
	charlie := srv.UserID("charlie")
	serverRoom := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, charlie))
	roomAlias := srv.MakeAliasMapping("flibble", serverRoom.RoomID)

	// join the room
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	alice.JoinRoom(t, roomAlias, nil)

	wantEventType := "m.room.message"

	waiter := NewWaiter()

	// TODO: Have a nicer api shape than just http.Handler
	srv.Mux().Handle("/_matrix/federation/v1/send/{txnID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer waiter.Finish()
		var body gomatrixserverlib.Transaction
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Errorf("failed to decode /send body: %s", err)
			w.WriteHeader(500)
			return
		}
		if len(body.PDUs) != 1 {
			t.Fatalf("got %d pdus, want %d", len(body.PDUs), 1)
		}
		ev, err := gomatrixserverlib.NewEventFromUntrustedJSON(body.PDUs[0], ver)
		if err != nil {
			t.Fatalf("PDU failed NewEventFromUntrustedJSON checks: %s", err)
		}
		if ev.Type() != wantEventType {
			t.Errorf("Wrong event type, got %s want %s", ev.Type(), wantEventType)
		}
		w.WriteHeader(200)
	})).Methods("PUT")

	alice.SendEventSynced(t, serverRoom.RoomID, b.Event{
		Type: wantEventType,
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world!",
		},
	})
	waiter.Wait(t, 5*time.Second)
}
