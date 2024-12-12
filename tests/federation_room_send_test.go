package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"golang.org/x/exp/slices"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
)

// TODO:
// Inbound federation can receive events
// Inbound federation can receive redacted events
// Ephemeral messages received from servers are correctly expired
// Events whose auth_events are in the wrong room do not mess up the room state

// Tests that the server is capable of making outbound /send requests
func TestOutboundFederationSend(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	waiter := helpers.NewWaiter()
	wantEventType := "m.room.message"

	// create a remote homeserver
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(
			// listen for PDU events in transactions
			func(ev gomatrixserverlib.PDU) {
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

	// the remote homeserver creates a public room
	ver := alice.GetDefaultRoomVersion(t)
	charlie := srv.UserID("charlie")
	serverRoom := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, charlie))
	roomAlias := srv.MakeAliasMapping("flibble", serverRoom.RoomID)

	// the local homeserver joins the room
	alice.MustJoinRoom(t, roomAlias, []string{deployment.GetConfig().HostnameRunningComplement})

	// the local homeserver sends an event into the room
	alice.SendEventSynced(t, serverRoom.RoomID, b.Event{
		Type: wantEventType,
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world!",
		},
	})

	// the remote homeserver then waits for the desired event to appear in a transaction
	waiter.Wait(t, 5*time.Second)
}

// Test ordering behaviour between /sync and /messages, when unexpected earlier federation events are injected.
// This aims to ensure that clients cannot miss events in the following case:
//
//	consider two homeservers in the same room starting at some event 0. Then:
//	  - network partition the servers
//	  - HS1 sends 1, HS2 sends 1'
//	  - a local user on HS1 sends 2,3,4
//	  - network partition is fixed. HS2 sends 1' to HS1.
//	  - a local user on HS1 sends 5,6,7
//	  - At this point, from HS1's pov, the streaming ordering is [1,2,3,4,1',5,6,7] and the topological order is [1 | 1', 2,3,4,5,6,7]
//	  - client requests timeline limit = 4 => [1',5,6,7] is returned because /sync is stream ordering. Everything is fine so far.
//	  - using the prev_batch token here in /messages SHOULD return 4,3,2,1, to ensure clients cannot missing 4,3,2.
//	    It may actually decide to filter topologically and just return 0, which would be incorrect.
//
// Additional notes from Erik who explains how it works _in Synapse_:
// > There are two orderings Synapse uses, called "stream ordering" and "topological ordering" (which is a specific topological ordering).
// Once an event is persisted these orderings do not change.
// > There are also two types of tokens: stream tokens and topological tokens. These correspond with segmenting events based on the
// stream ordering and the topological ordering respectively.
// > The two requests that happen are:
//  1. `/sync` which returns events in stream ordering and includes stream tokens for the start and end of the included set of events.
//     The events returned are the latest N events based on stream ordering in the room.
//  2. `/messages` which returns events in topological ordering and includes topological tokens for the start and end of the included
//     set of events. You can specify either stream or topological tokens for the upper and lower bounds of requested events.
//     The events returned are the latest N events based on topological ordering in the room, filtered by the tokens specified.
//
// > The question is: if we use a stream token from the start of the timeline in `/sync` to paginate via `/messages`, do we see all events?
// > Let's say we do a `/sync` and get a `prev_batch` stream token of `s`. When we do `/messages` this will return the set of events ordered
// > by topological ordering less than `s`, greatest first. i.e. there is a topological token `t`
// > (corresponding to the first event `/messages` returns) for which any event before `t` will be returned by `/messages` (except maybe if
// > the event is after `s`).
//
// > Given an arbitrary event `E` in the room, what cases are there and will it turn up in subsequent `/sync` or `/messages`?
//  1. If `E` is after `s` then it will come down `/sync`, and we're done.
//  2. If `E` is before `t` then it will come down `/messages` and we're done.
//  3. By construction of `t` there cannot be an event that is after `t` but before `s`, as `t` is defined to be a token that returns
//     all events before `s`. i.e., `E` must be in one of the two cases above.
//
// > This is true for the case where we receive `E` and *then* do the the pagination. What about the case where we have already started
// > paginating from a previous `/sync` request? For non-backfilled events then `E` must by definition of stream ordering happen after `s`,
// > and so come down `/sync` if there are no gaps. For the gappy sync case the client will have to paginate, but then the same rationale
// > as above applies.
func TestNetworkPartitionOrdering(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // 500s trying to /backfill
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	// create a remote homeserver
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(nil, nil),
	)
	srv.UnexpectedRequestsAreErrors = false // might get backfill requests
	cancel := srv.Listen()
	defer cancel()

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	// the remote homeserver creates a public room
	ver := alice.GetDefaultRoomVersion(t)
	charlie := srv.UserID("charlie")
	serverRoom := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, charlie))
	roomAlias := srv.MakeAliasMapping("flibble", serverRoom.RoomID)

	// the local homeserver joins the room
	alice.MustJoinRoom(t, roomAlias, []string{deployment.GetConfig().HostnameRunningComplement})
	bob.MustJoinRoom(t, roomAlias, []string{deployment.GetConfig().HostnameRunningComplement})
	// bob requests the last 4 timeline events. We don't care about it right now but do want the since token
	_, bobSince := bob.MustSync(t, client.SyncReq{
		Filter: `{"room":{"timeline":{"limit":4}}}`,
	})

	// create 1' on the remote homeserver but don't send it
	event1prime := srv.MustCreateEvent(t, serverRoom, federation.Event{
		Sender: charlie,
		Type:   "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Event 1'",
		},
	})
	serverRoom.AddEvent(event1prime)

	// send 1,2,3,4 on the local homeserver
	var eventIDs []string
	for i := 0; i < 4; i++ {
		eventIDs = append(eventIDs, alice.SendEventSynced(t, serverRoom.RoomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    fmt.Sprintf("event %d", i+1),
			},
		}))
	}

	// remote homeserver now injects event 1'
	srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{event1prime.JSON()}, nil)

	// ensure it gets there
	alice.MustSyncUntil(t, client.SyncReq{TimeoutMillis: "1000"}, client.SyncTimelineHasEventID(serverRoom.RoomID, event1prime.EventID()))

	// send 5,6,7
	for i := 5; i <= 7; i++ {
		eventIDs = append(eventIDs, alice.SendEventSynced(t, serverRoom.RoomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    fmt.Sprintf("event %d", i),
			},
		}))
	}
	t.Logf("events 1,2,3,4,5,6,7 = %v", eventIDs)

	// now bob requests timeline limit 4 in the room, should see 1',5,6,7
	res, _ := bob.MustSync(t, client.SyncReq{
		Filter: `{"room":{"timeline":{"limit":4}}}`,
		Since:  bobSince,
	})
	wantEventIDs := append([]string{
		event1prime.EventID()}, eventIDs[len(eventIDs)-3:]...,
	)
	i := 0
	timeline := res.Get(fmt.Sprintf("rooms.join.%s.timeline", client.GjsonEscape(serverRoom.RoomID)))
	must.MatchGJSON(t, timeline,
		match.JSONKeyArrayOfSize("events", 4),
		match.JSONArrayEach("events", func(r gjson.Result) error {
			wantEventID := wantEventIDs[i]
			gotEventID := r.Get("event_id").Str
			if gotEventID != wantEventID {
				return fmt.Errorf("got event id %v want %v", gotEventID, wantEventID)
			}
			if gotEventID == "" {
				return fmt.Errorf("missing event ID")
			}
			i++
			return nil
		}),
	)

	// now use the prev_batch token in /messages, we should see 4,3,2,1
	prevBatch := timeline.Get("prev_batch").Str
	must.NotEqual(t, prevBatch, "", "missing prev_batch")
	queryParams := url.Values{}
	queryParams.Set("dir", "b")
	queryParams.Set("limit", "4")
	queryParams.Set("from", prevBatch)
	wantEventIDs = eventIDs[0:4]
	slices.Reverse(wantEventIDs)
	t.Logf("want scrollback events %v", wantEventIDs)
	i = 0
	scrollbackRes := alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", serverRoom.RoomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, scrollbackRes, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyArrayOfSize("chunk", 4),
			match.JSONArrayEach("chunk", func(j gjson.Result) error {
				wantEventID := wantEventIDs[i]
				gotEventID := j.Get("event_id").Str
				if gotEventID != wantEventID {
					return fmt.Errorf("got event id %v want %v", gotEventID, wantEventID)
				}
				if gotEventID == "" {
					return fmt.Errorf("missing event ID")
				}
				i++
				return nil
			}),
		},
	})
}

func TestAllowInvalidTransactionPDU(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(nil, nil),
	)

	cancel := srv.Listen()
	defer cancel()

	nexy := srv.UserID("nexy")

	room := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"room_version": "10",
	})
	server_room := srv.MustJoinRoom(t, deployment, "hs1", room, nexy)


	event := srv.MustCreateEvent(t, server_room, federation.Event{
		Sender: nexy,
		Type: "m.room.message",
		Content: map[string]interface{}{"body": "hello i am nexy and i somehow broke the room into a v1 room"},
	})
	data, err := json.Marshal(event)
	if err != nil {
		t.Log("invalid json bwuh", err)
		t.FailNow()
	}
	data, err = sjson.SetBytes(data, "event_id", event.EventID())
	if err != nil {
		t.Log("failed to add event id to json dict")
		t.FailNow()
	}

	prev := event.PrevEventIDs()[0]
	v1_prev_events := [][]interface{}{
		{
			prev,
			map[string]interface{}{
				"sha256": "abase64encodedsha256hashshouldbe43byteslong",
			},
		},
	}

	data, err = sjson.SetBytes(data, "prev_events", v1_prev_events)

	client := srv.FederationClient(deployment)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	resp, err := client.SendTransaction(ctx, gomatrixserverlib.Transaction{
		TransactionID: gomatrixserverlib.TransactionID(fmt.Sprintf("complement-invalid-transaction-%d", time.Now().Nanosecond())),
		Origin: spec.ServerName(srv.ServerName()),
		Destination: "hs1",
		PDUs: []json.RawMessage{data},
		EDUs: nil,
	})

	if err != nil {
		ct.Fatalf(t, "Invalid PDU in transaction made the request fail")
	}

	for eventID, e := range resp.PDUs {
		if eventID != event.EventID() && eventID != event.EventID() {
			ct.Fatalf(t, "Server responded with bogus event ID %s", eventID)
		} else if e.Error == "" {
			ct.Fatalf(t, "Server accepted event sent into a V10 room using V1 format")
		} else {
			t.Logf("Correctly returned a 200 response including rejected event %s with error %s", eventID, e)
		}
	}

	if len(resp.PDUs) == 0 {
		t.Logf("INFO: Server didn't indicate an error when sending an invalid event id")
	}

}
