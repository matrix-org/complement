package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/complement/internal/must"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/tidwall/gjson"
)

// TODO:
// Outbound federation can request missing events
// Inbound federation can return missing events for $vis visibility
// outliers whose auth_events are in a different room are correctly rejected

// A homeserver receiving a response from `get_missing_events` for a version 6
// room with a bad JSON value (e.g. a float) should discard the bad data.
//
// To test this we need to:
// * Add an event with "bad" data into the room history, but don't send it.
// * Add a "good" event into the room history and send it.
// * The homeserver attempts to get the missing event (with the bad data).
// * Ensure that fetching the event results in an error.
func TestOutboundFederationIgnoresMissingEventWithBadJSONForRoomVersion6(t *testing.T) {
	deployment := must.Deploy(t, "federation_get_missing_events", b.BlueprintAlice)
	defer deployment.Destroy(t)
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleDirectoryLookups(),
	)
	cancel := srv.Listen()
	defer cancel()

	ver := gomatrixserverlib.RoomVersionV6
	charlie := srv.UserID("charlie")
	room := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, charlie))
	roomAlias := srv.Alias(room.RoomID, "flibble")
	// join the room
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	alice.MustDo(t, "POST", []string{"_matrix", "client", "r0", "join", roomAlias}, struct{}{})

	latestEvent := room.Timeline[len(room.Timeline)-1]

	// Sign this bad event which has a float (we can't use helpers here as they check it isn't bad)
	badEvent := b.Event{
		Type:   "m.room.message",
		Sender: charlie,
		Content: map[string]interface{}{
			"body":    "Message 1",
			"bad_val": 1.1,
		},
	}
	content, err := json.Marshal(badEvent.Content)
	if err != nil {
		t.Fatalf("failed to marshal badEvent content %+v", badEvent.Content)
	}
	eb := gomatrixserverlib.EventBuilder{
		Sender:     badEvent.Sender,
		Depth:      int64(room.Depth + 1), // depth starts at 1
		Type:       badEvent.Type,
		StateKey:   badEvent.StateKey,
		Content:    content,
		RoomID:     room.RoomID,
		PrevEvents: room.ForwardExtremities,
	}
	stateNeeded, err := gomatrixserverlib.StateNeededForEventBuilder(&eb)
	if err != nil {
		t.Fatalf("failed to work out auth_events : %s", err)
	}
	eb.AuthEvents = room.AuthEvents(stateNeeded)
	// we have to create this event as a v5 event which doesn't assert floats yet
	signedBadEvent, err := eb.Build(time.Now(), gomatrixserverlib.ServerName(srv.ServerName), srv.KeyID, srv.Priv, gomatrixserverlib.RoomVersionV5)
	if err != nil {
		t.Fatalf("failed to sign event: %s", err)
	}
	room.AddEvent(&signedBadEvent)

	sentEvent := srv.MustCreateEvent(t, room, b.Event{
		Type:   "m.room.message",
		Sender: charlie,
		Content: map[string]interface{}{
			"body": "Message 2",
		},
	})
	room.AddEvent(sentEvent)

	srv.Mux().HandleFunc("/_matrix/federation/v1/get_missing_events/{roomID}", func(w http.ResponseWriter, req *http.Request) {
		body := must.ParseJSON(t, req.Body)
		earliestEvents := gjson.GetBytes(body, "earliest_events")
		if !earliestEvents.IsArray() {
			t.Fatalf("get_missing_events 'earliest events' is not an array: %s", string(body))
		}
		early := earliestEvents.Array()
		if len(early) != 1 {
			t.Fatalf("get_missing_events 'earliest events' expected length 1, got %d", len(early))
		}
		must.EqualStr(t, early[0].Str, latestEvent.EventID(), "wrong earliest event ID")
		latestEvents := gjson.GetBytes(body, "latest_events")
		if !latestEvents.IsArray() {
			t.Fatalf("get_missing_events 'latest_events' is not an array: %s", string(body))
		}
		latest := latestEvents.Array()
		if len(latest) != 1 {
			t.Fatalf("get_missing_events 'latest_events' expected length 1, got %d", len(latest))
		}
		must.EqualStr(t, latest[0].Str, sentEvent.EventID(), "wrong latest event ID")
		// return the bad event, which should result in the transaction failing.
		w.WriteHeader(200)
		res := struct {
			Events []gomatrixserverlib.Event `json:"events"`
		}{
			Events: []gomatrixserverlib.Event{signedBadEvent},
		}
		b, err := json.Marshal(&res)
		must.NotError(t, "failed to marshal response", err)
		w.Write(b)
	}).Methods("POST")

	fedClient := srv.FederationClient(deployment, "hs1")
	resp, err := fedClient.SendTransaction(context.Background(), gomatrixserverlib.Transaction{
		TransactionID: "wut",
		Destination:   gomatrixserverlib.ServerName("hs1"),
		PDUs: []json.RawMessage{
			sentEvent.JSON(),
		},
	})
	must.NotError(t, "SendTransaction errored", err)
	if len(resp.PDUs) != 1 {
		t.Fatalf("got %d errors, want 1", len(resp.PDUs))
	}
	pduRes, ok := resp.PDUs[sentEvent.EventID()]
	if !ok {
		t.Fatalf("wrong PDU returned from send transaction, got %v want %s", resp.PDUs, sentEvent.EventID())
	}
	must.NotEqualStr(t, pduRes.Error, "", "wanted an error string for pdu but was blank")
}
