// +buaaild msc2836

package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/tidwall/gjson"
)

// This test checks that the homeserver makes a federated request to /event_relationships
// when walking a thread when it encounters an unknown event ID. The test configures a
// room on the Complement server with a thread which has following shape:
//     A
//    / \
//   B   C
//       |
//       D <- Test server joins here
//       |
//       E
// The test server is notified of event E in a /send transaction after joining the room.
// The client on the test server then hits /event_relationships with event ID 'E' and direction 'up'.
// This *should* cause the server to walk up the thread, realise it is missing event D and then ask
// another server in the room about event D via a federated /event_relationships call with D as the
// argument. This test then returns D,C,B,A to the server. This allows the client request to be satisfied
// and return D,C,A as the response (note: not B because it isn't a parent of D).
//
// We then check that B, which wasn't on the return path on the previous request, was persisted by calling
// /event_relationships again with event ID 'A' and direction 'down'.
func TestFederatedEventRelationships(t *testing.T) {
	deployment := Deploy(t, "msc2836_fed", b.BlueprintAlice)
	defer deployment.Destroy(t)
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
	)
	cancel := srv.Listen()
	defer cancel()

	// create a room on Complement, add some events to walk.
	roomVer := gomatrixserverlib.RoomVersionV6
	charlie := srv.UserID("charlie")
	room := srv.MustMakeRoom(t, roomVer, federation.InitialRoomEvents(roomVer, charlie))
	eventA := srv.MustCreateEvent(t, room, b.Event{
		Type:   "m.room.message",
		Sender: charlie,
		Content: map[string]interface{}{
			"body":    "[A] First message",
			"msgtype": "m.text",
		},
	})
	room.AddEvent(eventA)
	eventB := srv.MustCreateEvent(t, room, b.Event{
		Type:   "m.room.message",
		Sender: charlie,
		Content: map[string]interface{}{
			"body":    "[B] Top level reply 1",
			"msgtype": "m.text",
			"m.relationship": map[string]interface{}{
				"rel_type": "m.reference",
				"event_id": eventA.EventID(),
			},
		},
	})
	room.AddEvent(eventB)
	// wait 1ms to ensure that the timestamp changes, which is important when using the recent_first flag
	time.Sleep(1 * time.Millisecond)
	eventC := srv.MustCreateEvent(t, room, b.Event{
		Type:   "m.room.message",
		Sender: charlie,
		Content: map[string]interface{}{
			"body":    "[C] Top level reply 2",
			"msgtype": "m.text",
			"m.relationship": map[string]interface{}{
				"rel_type": "m.reference",
				"event_id": eventA.EventID(),
			},
		},
	})
	room.AddEvent(eventC)
	eventD := srv.MustCreateEvent(t, room, b.Event{
		Type:   "m.room.message",
		Sender: charlie,
		Content: map[string]interface{}{
			"body":    "[D] Second level reply",
			"msgtype": "m.text",
			"m.relationship": map[string]interface{}{
				"rel_type": "m.reference",
				"event_id": eventC.EventID(),
			},
		},
	})
	room.AddEvent(eventD)
	t.Logf("A: %s", eventA.EventID())
	t.Logf("B: %s", eventB.EventID())
	t.Logf("C: %s", eventC.EventID())
	t.Logf("D: %s", eventD.EventID())

	// we expect to be called with event D, and will return D,C,B,A
	waiter := NewWaiter()
	srv.Mux().HandleFunc("/_matrix/federation/unstable/event_relationships", func(w http.ResponseWriter, req *http.Request) {
		defer waiter.Finish()
		must.MatchRequest(t, req, match.HTTPRequest{
			JSON: []match.JSON{
				match.JSONKeyEqual("event_id", eventD.EventID()),
			},
		})

		eventsJSON := []json.RawMessage{
			eventD.JSON(),
			eventC.JSON(),
			eventB.JSON(),
			eventA.JSON(),
		}

		var chainJSON []json.RawMessage
		for _, ev := range room.AuthChain() {
			chainJSON = append(chainJSON, ev.JSON())
		}

		result := struct {
			Events    []json.RawMessage `json:"events"`
			Limited   bool              `json:"limited"`
			AuthChain []json.RawMessage `json:"auth_chain"`
		}{
			Events:    eventsJSON,
			Limited:   false,
			AuthChain: chainJSON,
		}
		resBytes, err := json.Marshal(result)
		must.NotError(t, "failed to marshal JSON", err)

		w.WriteHeader(200)
		w.Write(resBytes)
	})

	// join the room on HS1
	// HS1 will not have any of these messages, only the room state.
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	alice.JoinRoom(t, room.RoomID)

	// send a new child in the thread (child of D) so the HS has something to latch on to.
	eventE := srv.MustCreateEvent(t, room, b.Event{
		Type:   "m.room.message",
		Sender: charlie,
		Content: map[string]interface{}{
			"body":    "[E] Third level reply",
			"msgtype": "m.text",
			"m.relationship": map[string]interface{}{
				"rel_type": "m.reference",
				"event_id": eventD.EventID(),
			},
		},
	})
	room.AddEvent(eventE)
	fedClient := srv.FederationClient(deployment, "hs1")
	_, err := fedClient.SendTransaction(context.Background(), gomatrixserverlib.Transaction{
		TransactionID:  "complement",
		Origin:         gomatrixserverlib.ServerName(srv.ServerName),
		Destination:    gomatrixserverlib.ServerName("hs1"),
		OriginServerTS: gomatrixserverlib.AsTimestamp(time.Now()),
		PDUs: []json.RawMessage{
			eventE.JSON(),
		},
	})
	must.NotError(t, "failed to SendTransaction", err)

	// Hit /event_relationships to make sure it spiders the whole thing by asking /event_relationships on Complement
	res := alice.MustDo(t, "POST", []string{"_matrix", "client", "unstable", "event_relationships"}, map[string]interface{}{
		"event_id":  eventE.EventID(),
		"max_depth": 10,
		"direction": "up",
	})
	var gotEventIDs []string
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyEqual("limited", false),
			match.JSONArrayEach("events", func(r gjson.Result) error {
				eventID := r.Get("event_id").Str
				gotEventIDs = append(gotEventIDs, eventID)
				return nil
			}),
		},
	})
	waiter.Wait(t, time.Second)
	// we should have got events E,D,C,A (walk parents up to the top)
	must.HaveInOrder(t, gotEventIDs, []string{eventE.EventID(), eventD.EventID(), eventC.EventID(), eventA.EventID()})

	// now querying for the children of A should return A,B,C (it should've been remembered B from the previous /event_relationships request)
	res = alice.MustDo(t, "POST", []string{"_matrix", "client", "unstable", "event_relationships"}, map[string]interface{}{
		"event_id":     eventA.EventID(),
		"max_depth":    1,
		"direction":    "down",
		"recent_first": false,
	})
	gotEventIDs = []string{}
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyEqual("limited", false),
			match.JSONArrayEach("events", func(r gjson.Result) error {
				eventID := r.Get("event_id").Str
				gotEventIDs = append(gotEventIDs, eventID)
				return nil
			}),
		},
	})
	must.HaveInOrder(t, gotEventIDs, []string{eventA.EventID(), eventB.EventID(), eventC.EventID()})
}
