package tests

import (
	"context"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/must"

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
//   - /event_auth for the join rules event just returns the chain for the join rules event, which
//     just means it returns the auth_events as that is equal to the auth chain for this event.
//   - /event_auth for the latest join event returns the complete auth chain for Charlie (all the
//     joins and leaves are included), without any extraneous events.
func TestEventAuth(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	// create a remote homeserver which will make the /event_auth request
	var joinRuleEvent gomatrixserverlib.PDU
	waiter := helpers.NewWaiter()
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(
			// listen for the new join rule event
			func(ev gomatrixserverlib.PDU) {
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
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})
	room := srv.MustJoinRoom(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), roomID, charlie)
	firstJoinEvent := room.CurrentState("m.room.member", charlie)
	srv.MustLeaveRoom(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), roomID, charlie)
	leaveEvent := room.CurrentState("m.room.member", charlie)
	room = srv.MustJoinRoom(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), roomID, charlie)

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

	getEventAuth := func(t *testing.T, eventID string, wantAuthEventIDs []string) {
		t.Helper()
		t.Logf("/event_auth for %s - want %v", eventID, wantAuthEventIDs)
		fedClient := srv.FederationClient(deployment)
		eventAuthResp, err := fedClient.GetEventAuth(
			context.Background(),
			srv.ServerName(),
			deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
			room.Version,
			roomID,
			eventID,
		)
		must.NotError(t, "failed to /event_auth", err)
		if len(eventAuthResp.AuthEvents) == 0 {
			t.Fatalf("/event_auth returned 0 auth events")
		}
		gotAuthEvents := eventAuthResp.AuthEvents.UntrustedEvents(room.Version)
		if len(gotAuthEvents) != len(wantAuthEventIDs) {
			msg := "got:\n"
			for _, e := range gotAuthEvents {
				msg += e.EventID() + " : " + string(e.JSON()) + "\n\n"
			}
			t.Fatalf("got %d valid auth events (%d total), wanted %d.\n%s\nwant: %s", len(gotAuthEvents), len(eventAuthResp.AuthEvents), len(wantAuthEventIDs), msg, wantAuthEventIDs)
		}
		// make sure all the events match
		gotIDs := make([]string, len(gotAuthEvents))
		for i := range gotIDs {
			gotIDs[i] = gotAuthEvents[i].EventID()
		}
		must.ContainSubset(t, gotIDs, wantAuthEventIDs)
	}

	t.Run("returns auth events for the requested event", func(t *testing.T) {
		// now hit /event_auth for the join_rules event. The auth chain == the auth events.
		getEventAuth(t, joinRuleEvent.EventID(), joinRuleEvent.AuthEventIDs())
	})

	t.Run("returns the auth chain for the requested event", func(t *testing.T) {
		latestJoinEvent := room.CurrentState("m.room.member", charlie)
		// we want all the auth event IDs for the latest join event AND the previous leave and join events
		wantAuthEvents := latestJoinEvent.AuthEventIDs()
		wantAuthEvents = append(wantAuthEvents, firstJoinEvent.EventID(), leaveEvent.EventID())
		getEventAuth(t, latestJoinEvent.EventID(), wantAuthEvents)
	})
}
