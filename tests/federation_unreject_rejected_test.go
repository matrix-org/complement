package tests

import (
	"encoding/json"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/internal/federation"
)

// TestUnrejectRejectedEvents creates two events: A and B.
// To start with, we're going to withhold A from the homeserver
// and send event B. Event B should get rejected because event A
// is referred to as a prev event but is missing. Then we'll
// send event B again after sending event A. That should mean that
// event B is unrejected on the second pass and will appear in
// the /sync response AFTER event A.
func TestUnrejectRejectedEvents(t *testing.T) {
	deployment := complement.Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
	)
	srv.UnexpectedRequestsAreErrors = false
	cancel := srv.Listen()
	defer cancel()
	bob := srv.UserID("bob")

	// Create a new room on the federation server.
	ver := alice.GetDefaultRoomVersion(t)
	serverRoom := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, bob))

	// Join Alice to the new room on the federation server.
	alice.MustJoinRoom(t, serverRoom.RoomID, []string{srv.ServerName()})
	alice.MustSyncUntil(
		t, client.SyncReq{},
		client.SyncJoinedTo(alice.UserID, serverRoom.RoomID),
	)

	// Create the events. Event A will have whatever the current forward
	// extremities are as prev events. Event B will refer to event A only
	// to guarantee the test will work.
	eventA := srv.MustCreateEvent(t, serverRoom, federation.Event{
		Type:   "m.event.a",
		Sender: bob,
		Content: map[string]interface{}{
			"event": "A",
		},
	})
	eventB := srv.MustCreateEvent(t, serverRoom, federation.Event{
		Type:   "m.event.b",
		Sender: bob,
		Content: map[string]interface{}{
			"event": "B",
		},
		PrevEvents: []string{eventA.EventID()},
	})

	// Send event B into the room. Event A at this point is unknown
	// to the homeserver and we're not going to respond to the events
	// request for it, so it should get rejected.
	srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{eventB.JSON()}, nil)

	// Now we're going to send Event A into the room, which should give
	// the server the prerequisite event to pass Event B later. This one
	// should appear in /sync.
	srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{eventA.JSON()}, nil)

	// Wait for event A to appear in the room. We're going to store the
	// sync token here because we want to assert on the next sync that
	// we're only getting new events since this one (i.e. events after A).
	since := alice.MustSyncUntil(
		t, client.SyncReq{},
		client.SyncTimelineHasEventID(serverRoom.RoomID, eventA.EventID()),
	)

	// Finally, send Event B again. This time it should be unrejected and
	// should be sent as a new event down /sync for the first time.
	srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{eventB.JSON()}, nil)

	// Now see if event B appears in the room. Use the since token from the
	// last sync to ensure we're only waiting for new events since event A.
	alice.MustSyncUntil(
		t, client.SyncReq{Since: since},
		client.SyncTimelineHasEventID(serverRoom.RoomID, eventB.EventID()),
	)
}
