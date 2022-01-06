package tests

import (
	"context"
	"testing"
	"time"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/complement/internal/must"

	"github.com/matrix-org/gomatrixserverlib"
)

// Test basic functionality of /_matrix/federation/v1/event_auth/{roomId}/{eventId}
// and critically ensures that no extraneous events are returned
// this was a dendrite bug, see https://github.com/matrix-org/dendrite/issues/2084
//
// This test works by configuring the following room:
// - New room over federation between the HS and Complement
// - Charlie on Complement joins the room over federation, then leaves, then rejoins
// - Alice updates join rules for the room (test waits until it sees this event over federation)
// At this point we can then test:
// - /event_auth for the join rules event just returns the chain for the join rules event, which
//   just means it returns the auth_events as that is equal to the auth chain for this event.
// - /event_auth for the latest join event returns the complete auth chain for Charlie (all the
//   joins and leaves are included), without any extraneous events.
func TestEventAuth(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	// create a remote homeserver which will make the /event_auth request
	var joinRuleEvent *gomatrixserverlib.Event
	waiter := NewWaiter()
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(
			// listen for the new join rule event
			func(ev *gomatrixserverlib.Event) {
				if jr, _ := ev.JoinRule(); jr == "invite" {
					joinRuleEvent = ev
					waiter.Finish()
				}
			},
			nil,
		),
	)
	srv.UnexpectedRequestsAreErrors = false // we expect to be pushed events
	cancel := srv.Listen()
	defer cancel()

	// make a room and join it
	charlie := srv.UserID("charlie")
	roomID := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})
	room := srv.MustJoinRoom(t, deployment, "hs1", roomID, charlie)
	/*
		srv.MustLeaveRoom(t, deployment, "hs1", roomID, charlie)
		room = srv.MustJoinRoom(t, deployment, "hs1", roomID, charlie) */

	// now update the auth chain a bit: dendrite had a bug where it returned the auth chain for all
	// the current state in addition to the event asked for
	alice.SendEventSynced(t, roomID, b.Event{
		Type:     "m.room.join_rules",
		StateKey: b.Ptr(""),
		Content: map[string]interface{}{
			"join_rule": "invite",
		},
	})
	waiter.Wait(t, 1*time.Second) // wait for the join rule to make it to the complement server

	// now hit /event_auth
	resp, err := srv.FederationClient(deployment).GetEventAuth(context.Background(), "hs1", room.Version, roomID, joinRuleEvent.EventID())
	must.NotError(t, "failed to /event_auth", err)
	if len(resp.AuthEvents) == 0 {
		t.Fatalf("/event_auth returned 0 auth events")
	}
	if len(resp.AuthEvents) != len(joinRuleEvent.AuthEvents()) {
		msg := "got:\n"
		for _, e := range resp.AuthEvents {
			msg += string(e.JSON()) + "\n\n"
		}
		t.Fatalf("got %d auth events, wanted %d.\n%s\nwant: %s", len(resp.AuthEvents), len(joinRuleEvent.AuthEvents()), msg, joinRuleEvent.AuthEventIDs())
	}
	// make sure all the events match
	wantIDs := map[string]bool{}
	for _, id := range joinRuleEvent.AuthEventIDs() {
		wantIDs[id] = true
	}
	for _, e := range resp.AuthEvents {
		delete(wantIDs, e.EventID())
	}
	if len(wantIDs) > 0 {
		t.Errorf("missing events %v", wantIDs)
	}
}
