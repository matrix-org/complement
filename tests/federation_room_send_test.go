package tests

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/complement/internal/must"
	"github.com/matrix-org/gomatrixserverlib"
)

// TODO:
// Inbound federation can receive events
// Inbound federation can receive redacted events
// Ephemeral messages received from servers are correctly expired
// Events whose auth_events are in the wrong room do not mess up the room state

// Tests that the server is capable of making outbound /send requests
func TestOutboundFederationSend(t *testing.T) {
	deployment := must.Deploy(t, "federation_send", b.BlueprintAlice)
	defer deployment.Destroy(t)

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

	wantEventType := "m.room.message"

	// TODO: Have 'await' with a timeout
	var wg sync.WaitGroup
	wg.Add(1)

	// TODO: Have a nicer api shape than just http.Handler
	srv.Mux().Handle("/_matrix/federation/v1/send/{txnID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer wg.Done()
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

	alice.MustDo(t, "PUT", []string{"_matrix", "client", "r0", "rooms", serverRoom.RoomID, "send", wantEventType, "1"}, struct {
		Msgtype string `json:"msgtype"`
		Body    string `json:"body"`
	}{
		Msgtype: "m.text",
		Body:    "Hello world!",
	})
	t.Logf("alice sent message")
	wg.Wait()
}
