package tests

import (
	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/complement/runtime"
	"github.com/matrix-org/gomatrixserverlib"
	"testing"
	"time"
)

// test that a redaction is sent out over federation even if we don't have the original event
func TestFederationRedactSendsWithoutEvent(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite)

	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	waiter := NewWaiter()
	wantEventType := "m.room.redaction"

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

	// create username
	charlie := srv.UserID("charlie")

	ver := alice.GetDefaultRoomVersion(t)

	// the remote homeserver creates a public room allowing anyone to redact
	initalEvents := federation.InitialRoomEvents(ver, charlie)
	plEvent := initalEvents[2]
	plEvent.Content["redact"] = 0
	serverRoom := srv.MustMakeRoom(t, ver, initalEvents)
	roomAlias := srv.MakeAliasMapping("flibble", serverRoom.RoomID)

	// the local homeserver joins the room
	alice.JoinRoom(t, roomAlias, []string{srv.ServerName()})

	// inject event to redact in the room
	badEvent := srv.MustCreateEvent(t, serverRoom, b.Event{
		Type:   "m.room.message",
		Sender: charlie,
		Content: map[string]interface{}{
			"body": "666",
		},
	})
	serverRoom.AddEvent(badEvent)

	eventID := badEvent.EventID()
	fullServerName := srv.ServerName()
	eventToRedact := eventID + ":" + fullServerName

	// the client sends a request to the local homeserver to send the redaction
	res := alice.SendRedaction(t, serverRoom.RoomID, b.Event{Type: wantEventType, Content: map[string]interface{}{
		"msgtype": "m.room.redaction"},
		Redacts: eventToRedact}, eventToRedact)

	// wait for redaction to arrive at remote homeserver
	waiter.Wait(t, 1*time.Second)

	// Check that the last event in the room is now the redaction
	lastEvent := serverRoom.Timeline[len(serverRoom.Timeline)-1]
	lastEventType := lastEvent.Type()
	wantedType := "m.room.redaction"
	if lastEventType != wantedType {
		t.Fatalf("Incorrent event type %s, wanted m.room.redaction.", lastEventType)
	}

	// check that the event id of the redaction sent by alice is the same as the redaction event in the room
	if res != lastEvent.EventID() {
		t.Fatalf("Incorrent event id %s, wanted %s.", res, lastEvent.EventID())
	}

}
