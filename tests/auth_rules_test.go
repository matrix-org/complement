package tests

import (
	"fmt"
	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/gomatrixserverlib"
	"net/url"
	"testing"
)

// Tests which ensure the specifics of the auth rules are upheld.
//
// I have chosen to test V9, since that is the most recent at the time of
// writing.
//
// The general strategy is:
// 1. Join the server under test (SUT) to a complement-controlled room.
// 2. Send SUT a bad event whose parent is the join event from step 1.
// 3. Send SUT a good event (e.g. a simple "m.room.message") whose parent
//    is also the join event from step 1.
// 4. Have a complement-controlled client sync until it sees the good event
//    from step 3. Assert that the timeline does not contain the bad event
//    from step 2.
// 5. Call the federation `GET /event/<BAD>` endpoint on SUT to confirm
//    that the event is not accepted.
//
// This doesn't work for some special cases (e.g. the auth rules as applied
// right after the room is created). We take an ad-hoc approach on a
// case-by-case basis.

// Test that the server under test (SUT) rejects a create event which has a
// parent event.
//
// This is Rule 1.1 of the v9 auth rules.
//
// Because m.room.create must be the first event in a room, the general strategy
// will not work. Instead:
// - Use the CS API to have the SUT perform a remote-join handshake.
// - In the send_join response from complement, include a dodgy m.room.create
//   event in the auth chain
// -

func TestCreateEventCannotHaveParentEvents(t *testing.T) {
	// Deploy SUT.
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	ver := alice.GetDefaultRoomVersion(t)

	// Create the complement-controlled homeserver.
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
	)
	srv.UnexpectedRequestsAreErrors = false // ignore presence EDUs from SUT
	cancel := srv.Listen()
	defer cancel()

	// Create a room with a malformed m.room.create event.
	creator := srv.UserID("creator")
	events := federation.InitialRoomEvents(ver, creator)
	createEvent := &events[0]
	createEvent.PrevEvents = []string{"!nonexistentPrevEvent:" + srv.ServerName()}
	room := srv.MustMakeRoom(t, ver, events)

	// Extract the signed version of this event, so we can retrieve the event ID.
	signedCreateEvent := room.CurrentState("m.room.create", "")
	if signedCreateEvent == nil {
		t.Fatalf("signedCreateEvent is nil")
	}

	// Have the SUT start a remote join handshake.
	// TODO: the join ought to fail, but it succeeds under Synapse
	alice.JoinRoom(t, room.RoomID, []string{srv.ServerName()})

	// Request the bad event from SUT.
	req := gomatrixserverlib.NewFederationRequest(
		"GET",
		"hs1",
		fmt.Sprintf("/_matrix/federation/v1/event/%s", url.QueryEscape(signedCreateEvent.EventID())),
	)
	var resBody interface{}
	// SUT should not have provided us with an event.
	err := srv.SendFederationRequest(t, deployment, req, &resBody)
	// TODO specify that we expect a gomatrix.RespError here with a given error code.
	if err == nil {
		t.Fatalf(
			"Expected an error when retrieving the bad create event; got no error."+
				"\nResponse was: %s", resBody)
	}

}
