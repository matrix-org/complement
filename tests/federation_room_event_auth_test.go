// These tests currently fail on Dendrite, due to Dendrite bugs.
//go:build !dendrite_blacklist
// +build !dendrite_blacklist

package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/complement/internal/must"
)

func TestInboundFederationRejectsEventsWithRejectedAuthEvents(t *testing.T) {
	/* These tests check that events which refer to rejected events in auth_events
	 * are themselves rejected.
	 *
	 * They are regression tests for https://github.com/matrix-org/synapse/issues/9595.
	 *
	 * In order to inject an outlier, we include it as an extra auth_event in a
	 * regular event. Doing so means that the regular event should itself be
	 * rejected.
	 *
	 * We actually send two such events. On one of them, we reply to the
	 * incoming /event_auth request with the bogus outlier in
	 * the auth_events; for the other, we return a 404. This means we can
	 * exercise different code paths in Synapse.
	 *
	 * We finish up by sending a final, normal, event which should be accepted
	 * everywhere. This acts as a sentinel so that we can be sure that the
	 * events have all been correctly propagated.
	 *
	 * The DAG ends up looking like this:
	 *
	 *       C
	 *     / | \
	 *    /  R  \
	 *   |   ^   \
	 *   |   ... O
	 *   |       ^
	 *   X .......
	 *   |       ^
	 *   Y .......
	 *   |
	 *   S
	 *
	 * Where:
	 *    |  represents a "prev_event" link (older events are at the top)
	 *    .... represents an "auth_event" link
	 *    C is the room creation series
	 *    R is a rejected event
	 *    O is an outlier, which should be rejected
	 *    X is an event with O among its auth_events, which should be rejected
	 *           as a side-effect of O being rejected
	 *    Y is a second regular event with O in its auth_events, but we give a
	 *           different reply to /event_auth
	 *    S is the final regular event, which acts as a sentinel
	 *
	 * To check if the outlier is rejected, we simply request the event via
	 * /rooms/{roomID}/event. If it is rejected, we should get a 404.
	 */

	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),

		// accept incoming presence transactions, etc
		federation.HandleTransactionRequests(nil, nil),

		// accept incoming /event requests (dendrite asks for the outlier via /event; synapse calls /event_auth instead)
		federation.HandleEventRequests(),
	)
	cancel := srv.Listen()
	defer cancel()
	fedClient := srv.FederationClient(deployment)

	/* Create a handler for /event_auth */
	// a map from event ID to events to be returned by /event_auth
	eventAuthMap := make(map[string][]*gomatrixserverlib.Event)
	srv.Mux().HandleFunc("/_matrix/federation/v1/event_auth/{roomID}/{eventID}", func(w http.ResponseWriter, req *http.Request) {
		vars := mux.Vars(req)
		eventID := vars["eventID"]
		authEvents, ok := eventAuthMap[eventID]
		if !ok {
			t.Logf("Unexpected /event_auth request for event %s", eventID)
			w.WriteHeader(404)
			_, _ = w.Write([]byte("{}"))
			return
		}
		res := gomatrixserverlib.RespEventAuth{AuthEvents: authEvents}
		responseBytes, _ := json.Marshal(&res)
		w.WriteHeader(200)
		_, _ = w.Write(responseBytes)
	}).Methods("GET")

	// have Alice create a room, and then join it
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	testRoomID := alice.CreateRoom(t, struct {
		Preset string `json:"preset"`
	}{
		"public_chat",
	})
	charlie := srv.UserID("charlie")
	room := srv.MustJoinRoom(t, deployment, "hs1", testRoomID, charlie)
	charlieMembershipEvent := room.CurrentState("m.room.member", charlie)

	// have Charlie send a PL event which will be rejected
	rejectedEvent := srv.MustCreateEvent(t, room, b.Event{
		Type:     "m.room.power_levels",
		StateKey: b.Ptr(""),
		Sender:   charlie,
		Content: map[string]interface{}{
			"users": map[string]interface{}{},
		},
	})
	_, err := fedClient.SendTransaction(context.Background(), gomatrixserverlib.Transaction{
		TransactionID:  "complement1",
		Origin:         gomatrixserverlib.ServerName(srv.ServerName()),
		Destination:    "hs1",
		OriginServerTS: gomatrixserverlib.AsTimestamp(time.Now()),
		PDUs: []json.RawMessage{
			rejectedEvent.JSON(),
		},
	})
	must.NotError(t, "failed to SendTransaction", err)
	t.Logf("Sent rejected PL event %s", rejectedEvent.EventID())

	// create an event to be pulled in as an outlier, which is valid according to its prev events,
	// but uses the rejected event among its auth events.
	outlierEvent := srv.MustCreateEvent(t, room, b.Event{
		Type:     "m.room.member",
		StateKey: &charlie,
		Sender:   charlie,
		Content:  map[string]interface{}{"membership": "join", "test": 1},
		AuthEvents: []string{
			room.CurrentState("m.room.create", "").EventID(),
			room.CurrentState("m.room.join_rules", "").EventID(),
			rejectedEvent.EventID(),
			charlieMembershipEvent.EventID(),
		},
	})
	// add it to room.Timeline so that HandleEventRequests() can find it, but
	// don't use room.AddEvent(), because we don't want it to be a forward extremity.
	room.Timeline = append(room.Timeline, outlierEvent)
	t.Logf("Created outlier event %s", outlierEvent.EventID())

	// create a regular event which refers to the outlier event in its auth events,
	// so that the outlier gets pulled in.
	sentEventAuthEvents := []*gomatrixserverlib.Event{
		room.CurrentState("m.room.create", ""),
		room.CurrentState("m.room.join_rules", ""),
		room.CurrentState("m.room.power_levels", ""),
		charlieMembershipEvent,
		outlierEvent,
	}
	sentEvent1 := srv.MustCreateEvent(t, room, b.Event{
		Type:       "m.room.message",
		Sender:     charlie,
		Content:    map[string]interface{}{"body": "sentEvent1"},
		AuthEvents: room.EventIDsOrReferences(sentEventAuthEvents),
	})
	room.AddEvent(sentEvent1)
	eventAuthMap[sentEvent1.EventID()] = sentEventAuthEvents
	t.Logf("Created sent event 1 %s", sentEvent1.EventID())

	// another a regular event which refers to the outlier event, but
	// this time we will give a different answer to /event_auth
	sentEvent2 := srv.MustCreateEvent(t, room, b.Event{
		Type:       "m.room.message",
		Sender:     charlie,
		Content:    map[string]interface{}{"body": "sentEvent1"},
		AuthEvents: room.EventIDsOrReferences(sentEventAuthEvents),
	})
	room.AddEvent(sentEvent2)
	// we deliberately add nothing to eventAuthMap for this event, to make /event_auth
	// return a 404.
	t.Logf("Created sent event 2 %s", sentEvent2.EventID())

	// finally, a genuine regular event.
	sentinelEvent := srv.MustCreateEvent(t, room, b.Event{
		Type:    "m.room.message",
		Sender:  charlie,
		Content: map[string]interface{}{"body": "sentinelEvent"},
	})
	t.Logf("Created sentinel event %s", sentinelEvent.EventID())

	_, err = fedClient.SendTransaction(context.Background(), gomatrixserverlib.Transaction{
		TransactionID:  "complement2",
		Origin:         gomatrixserverlib.ServerName(srv.ServerName()),
		Destination:    "hs1",
		OriginServerTS: gomatrixserverlib.AsTimestamp(time.Now()),
		PDUs: []json.RawMessage{
			sentEvent1.JSON(),
			sentEvent2.JSON(),
			sentinelEvent.JSON(),
		},
	})
	must.NotError(t, "failed to SendTransaction", err)
	t.Logf("Sent transaction; awaiting arrival")

	// wait for alice to receive sentinelEvent
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(
		room.RoomID,
		func(ev gjson.Result) bool {
			return ev.Get("event_id").Str == sentinelEvent.EventID()
		},
	))

	// now inspect the results. Each of the rejected events should give a 404 for /event
	t.Run("Outlier should be rejected", func(t *testing.T) {
		res := alice.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", room.RoomID, "event", outlierEvent.EventID()})
		defer res.Body.Close()
		if res.StatusCode != 404 {
			t.Errorf("Expected a 404 when fetching outlier event, but got %d", res.StatusCode)
		}
	})

	t.Run("sent event 1 should be rejected", func(t *testing.T) {
		res := alice.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", room.RoomID, "event", sentEvent1.EventID()})
		defer res.Body.Close()
		if res.StatusCode != 404 {
			t.Errorf("Expected a 404 when fetching sent event 1, but got %d", res.StatusCode)
		}
	})

	t.Run("sent event 2 should be rejected", func(t *testing.T) {
		res := alice.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", room.RoomID, "event", sentEvent2.EventID()})
		defer res.Body.Close()
		if res.StatusCode != 404 {
			t.Errorf("Expected a 404 when fetching sent event 2, but got %d", res.StatusCode)
		}
	})
}
