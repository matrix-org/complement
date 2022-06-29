package tests

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/matrix-org/gomatrixserverlib"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

// Tests which ensure the specifics of the auth rules are upheld.

// I have chosen to test V9, since that is the most recent supported by Synapse at the time of writing.
const testRoomVersion = gomatrixserverlib.RoomVersionV9

/* badEventFactory is the type of a function to be passed to testBadEvent
 *
 * It should create an event in the given room. If "badEvent" is false, it should create a "control" event which should
 * be accepted. If "badEvent" is true, it should create a "bad" event which should be rejected
 */
type badEventFactory func(badEvent bool, deployment *docker.Deployment, srv *federation.Server, room *federation.ServerRoom) *gomatrixserverlib.Event

/* testBadEvent is a helper for running the event auth tests.
 *
 * The strategy is:
 *
 * 1. Join the server under test (SUT) to a complement-controlled room.
 * 2. Send SUT a "control" event which should be accepted.
 * 3. Send SUT a bad event, as similar as possible to the "control" event.
 * 4. Send SUT a good event (e.g. a simple "m.room.message").
 * 5. Have a complement-controlled client sync until it sees the good event from step 4.
 *    Assert that the timeline *does* contain the control event from step 2.
 *    Assert that the timeline does not contain the bad event from step 3.
 * 6. Call the C-S API on SUT to fetch the bad event to confirm that the event is not accepted.
 *
 * The control event is important to ensure that we are checking what we think we are, and that the bad event isn't
 * being rejected for some other reason.
 *
 * The caller must pass a factory which creates the control and bad event in step 2.
 */
func testBadEvent(t *testing.T, badEventFactory badEventFactory) {
	// Deploy SUT.
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	// Create the complement-controlled homeserver.
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		// accept incoming presence transactions, etc
		federation.HandleTransactionRequests(nil, nil),
	)
	cancel := srv.Listen()
	defer cancel()

	// create a room on the complement side. This is important, as some of the checks (notably those around
	// m.room.create) require the room id to match the sender user id.
	charlie := srv.UserID("charlie")
	room := srv.MustMakeRoom(t, testRoomVersion, federation.InitialRoomEvents(testRoomVersion, charlie))

	// Have the SUT start a remote join handshake.
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	alice.JoinRoom(t, room.RoomID, []string{srv.ServerName()})

	// create the control event
	controlEvent := badEventFactory(false, deployment, srv, room)
	t.Logf("Created control event %s", controlEvent.EventID())

	// create the bad event
	badEvent := badEventFactory(true, deployment, srv, room)
	t.Logf("Created bad event %s", badEvent.EventID())

	// create a regular event to act as a sentinel
	sentinelEvent := srv.MustCreateEvent(t, room, b.Event{
		Type:    "m.room.message",
		Sender:  charlie,
		Content: map[string]interface{}{"body": "sentinelEvent"},
	})
	room.AddEvent(sentinelEvent)
	t.Logf("Created sentinel event %s", sentinelEvent.EventID())

	// send all the events over and wait for Alice to get the sentinel
	srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{
		controlEvent.JSON(),
		badEvent.JSON(),
		sentinelEvent.JSON(),
	}, nil)
	t.Logf("Sent transaction; awaiting arrival")

	// wait for alice to receive sentinelEvent
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(room.RoomID, sentinelEvent.EventID()))

	// do a full sync, and check we *do* see the control event, and *don't* see the bad event.
	response, _ := alice.MustSync(t, client.SyncReq{})
	timelineEvents := response.Get("rooms.join." + client.GjsonEscape(room.RoomID) + ".timeline.events").Array()
	seenControlEvent := false
	for _, ev := range timelineEvents {
		if ev.Get("event_id").Str == controlEvent.EventID() {
			seenControlEvent = true
		}
		if ev.Get("event_id").Str == badEvent.EventID() {
			t.Errorf("Found bad event in sync response")
		}
	}
	if !seenControlEvent {
		t.Errorf("Did not receive control event in sync response - maybe this test is not valid?")
	}

	// try to fetch the bad event, and check we get a 404
	res := alice.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", room.RoomID, "event", badEvent.EventID()})
	defer res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("Expected a 404 when fetching bad auth event, but got %d", res.StatusCode)
	}
}

/* testRoomWithBadInitialEvents is a second helper, specifically for testing problems in the initial events in a room.
 *
 * Because m.room.create must be the first event in a room, the general strategy is not reliable, so we also try this
 * second strategy:
 *
 * 1. Create a room on the complement side, with bogus initial events.
 * 2. Use the CS API to have the SUT perform a remote-join handshake. Return the bad initial events in the send_join
 *    response from complement.
 * 3. We expect the CS join request to fail with a 400.
 */
func testRoomWithBadInitialEvents(t *testing.T, initialEventFactory func(srv *federation.Server) []b.Event) {
	// Deploy SUT.
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")

	// Create the complement-controlled homeserver.
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		// accept incoming presence transactions, etc
		federation.HandleTransactionRequests(nil, nil),
	)
	cancel := srv.Listen()
	defer cancel()

	// Create a room with a malformed m.room.create event.
	room := srv.MustMakeRoom(t, testRoomVersion, initialEventFactory(srv))

	// Have the SUT attempt a remote join handshake. It should fail with a 400.
	query := url.Values{}
	query.Add("server_name", srv.ServerName())
	res := alice.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "join", room.RoomID}, client.WithQueries(query))
	must.MatchResponse(t, res, match.HTTPResponse{StatusCode: 400})
}

/*******************************************************************************
 *
 * Tests for m.room.create events
 */

// Test that the server under test (SUT) rejects a create event which has a parent event.
//
// This is Rule 1.1 of https://spec.matrix.org/v1.3/rooms/v9/#authorization-rules
func TestCreateEventCannotHaveParentEvents(t *testing.T) {
	t.Run("Cannot join a room with a bad create event", func(t *testing.T) {
		testRoomWithBadInitialEvents(t, func(srv *federation.Server) []b.Event {
			creator := srv.UserID("creator")
			events := federation.InitialRoomEvents(testRoomVersion, creator)
			createEvent := &events[0]
			createEvent.PrevEvents = []string{"!nonexistentPrevEvent:" + srv.ServerName()}
			return events
		})
	})

	t.Run("Bad create events are rejected in existing rooms", func(t *testing.T) {
		testBadEvent(t, func(badEvent bool, deployment *docker.Deployment, srv *federation.Server, room *federation.ServerRoom) *gomatrixserverlib.Event {
			charlie := srv.UserID("charlie")
			event := b.Event{
				Type:     "m.room.create",
				StateKey: b.Ptr(""),
				Sender:   charlie,
				Content: map[string]interface{}{
					"creator":      charlie,
					"room_version": testRoomVersion,
				},
				AuthEvents: []string{},
			}
			if badEvent {
				// MustCreateEvent will give the event prev_events automatically
			} else {
				event.PrevEvents = []string{}
			}

			createEvent := srv.MustCreateEvent(t, room, event)
			// we don't add it to the room state, otherwise the sentinel event gets
			// rejected too.
			return createEvent
		})
	})
}

/*******************************************************************************
 *
 * Tests for auth events (rules 2.1 and 2.2)
 */

func TestInboundFederationRejectsEventsWithDuplicatedAuthEvents(t *testing.T) {
	// Rule 2.1 in the auth rules (https://spec.matrix.org/v1.3/rooms/v9/#authorization-rules) says:
	//
	// Reject if event has auth_events that ... have duplicate entries for a given type and state_key pair
	//
	// We create such an event, and check it gets rejected

	var membershipEvent1, membershipEvent2 *gomatrixserverlib.Event

	testBadEvent(t, func(badEvent bool, deployment *docker.Deployment, srv *federation.Server, room *federation.ServerRoom) *gomatrixserverlib.Event {
		charlie := srv.UserID("charlie")

		// have charlie send a second membership event (but hang onto the existing one first)
		if membershipEvent1 == nil {
			membershipEvent1 = room.CurrentState("m.room.member", charlie)
		}
		if membershipEvent2 == nil {
			membershipEvent2 = srv.MustCreateEvent(t, room, b.Event{
				Type:     "m.room.member",
				StateKey: &charlie,
				Sender:   charlie,
				Content: map[string]interface{}{
					"membership":   "join",
					"test_content": "test",
				},
			})
			room.AddEvent(membershipEvent2)
			srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{
				membershipEvent2.JSON(),
			}, nil)
		}

		var authEvents []*gomatrixserverlib.Event
		if badEvent {
			// create a regular event which refers to both membership events rules
			authEvents = []*gomatrixserverlib.Event{
				room.CurrentState("m.room.create", ""),
				room.CurrentState("m.room.power_levels", ""),
				membershipEvent1, membershipEvent2,
			}
		} else {
			// just use the current membership event
			authEvents = []*gomatrixserverlib.Event{
				room.CurrentState("m.room.create", ""),
				room.CurrentState("m.room.power_levels", ""),
				membershipEvent2,
			}
		}
		event := srv.MustCreateEvent(t, room, b.Event{
			Type:       "m.room.message",
			Sender:     charlie,
			Content:    map[string]interface{}{"body": "event"},
			AuthEvents: room.EventIDsOrReferences(authEvents),
		})
		room.AddEvent(event)
		return event
	})
}

func TestInboundFederationRejectsEventsWithExcessAuthEvents(t *testing.T) {
	// Rule 2.2 in the auth rules (https://spec.matrix.org/v1.3/rooms/v9/#authorization-rules) says:
	//
	// Reject if event has auth_events that ... have entries whose type and state_key don’t
	// match those specified by the auth events selection algorithm described in the server specification.
	//
	// We create such an event, and check it gets rejected

	testBadEvent(t, func(badEvent bool, deployment *docker.Deployment, srv *federation.Server, room *federation.ServerRoom) *gomatrixserverlib.Event {
		charlie := srv.UserID("charlie")

		var authEvents []*gomatrixserverlib.Event

		// normally we need the create, PLs, and membership events.
		authEvents = []*gomatrixserverlib.Event{
			room.CurrentState("m.room.create", ""),
			room.CurrentState("m.room.power_levels", ""),
			room.CurrentState("m.room.member", charlie),
		}

		// for the bad event, include the join rules too
		if badEvent {
			authEvents = append(authEvents,
				room.CurrentState("m.room.join_rules", ""))
		}

		event := srv.MustCreateEvent(t, room, b.Event{
			Type:       "m.room.message",
			Sender:     charlie,
			Content:    map[string]interface{}{"body": "event"},
			AuthEvents: room.EventIDsOrReferences(authEvents),
		})
		room.AddEvent(event)
		return event
	})
}
