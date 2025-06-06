package tests

import (
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
)

// test that a redaction is sent out over federation even if we don't have the original event
func TestFederationRedactSendsWithoutEvent(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite)

	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	waiter := helpers.NewWaiter()
	wantEventType := "m.room.redaction"

	// create a remote homeserver
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(
			// listen for PDU events in transactions
			func(ev gomatrixserverlib.PDU) {
				defer waiter.Finish()

				must.Equal(t, ev.Type(), wantEventType, "wrong event type")
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
	alice.MustJoinRoom(t, roomAlias, []spec.ServerName{srv.ServerName()})

	// inject event to redact in the room
	badEvent := srv.MustCreateEvent(t, serverRoom, federation.Event{
		Type:   "m.room.message",
		Sender: charlie,
		Content: map[string]interface{}{
			"body": "666",
		}})
	serverRoom.AddEvent(badEvent)

	eventID := badEvent.EventID()
	fullServerName := srv.ServerName()
	eventToRedact := eventID + ":" + string(fullServerName)

	// the client sends a request to the local homeserver to send the redaction
	redactionEventID := alice.MustSendRedaction(t, serverRoom.RoomID, map[string]interface{}{
		"reason": "reasons...",
	}, eventToRedact)

	// wait for redaction to arrive at remote homeserver
	waiter.Wait(t, 1*time.Second)

	// Check that the last event in the room is now the redaction
	lastEvent := serverRoom.Timeline[len(serverRoom.Timeline)-1]
	lastEventType := lastEvent.Type()
	must.Equal(t, lastEventType, "m.room.redaction", "incorrect event type")

	// check that the event id of the redaction sent by alice is the same as the redaction event in the room
	must.Equal(t, lastEvent.EventID(), redactionEventID, "incorrect event id")
}
