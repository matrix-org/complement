// +build msc2836

package tests

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/matrix-org/gomatrixserverlib"
	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

// This test checks that federated threading works when the remote server joins after the messages
// have been sent. The test configures a thread like:
//    A
//    |
//    B
//   / \
//  C   D
// Then a remote server joins the room. /event_relationships is then hit with event ID 'D' which the
// joined server does not have. This should cause a remote /event_relationships request to service the
// request. The request parameters will pull in events D,B. This gets repeated for a second time with
// an event which the server does have, event B, to ensure that this request also works and also does
// federated hits to return missing events (A,C).
func TestEventRelationships(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationOneToOneRoom)
	defer deployment.Destroy(t)

	// Create the room and send events A,B,C,D
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	roomID := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})
	eventA := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message A",
		},
	})
	eventB := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message B",
			"m.relationship": map[string]interface{}{
				"rel_type": "m.reference",
				"event_id": eventA,
			},
		},
	})
	eventC := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message C",
			"m.relationship": map[string]interface{}{
				"rel_type": "m.reference",
				"event_id": eventB,
			},
		},
	})
	eventD := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message D",
			"m.relationship": map[string]interface{}{
				"rel_type": "m.reference",
				"event_id": eventB,
			},
		},
	})
	t.Logf("Event ID A:%s  B:%s  C:%s  D:%s", eventA, eventB, eventC, eventD)

	// Join the room from another server
	bob := deployment.Client(t, "hs2", "@bob:hs2")
	_ = bob.JoinRoom(t, roomID, []string{"hs1"})

	// Now hit /event_relationships with eventD
	res := bob.MustDo(t, "POST", []string{"_matrix", "client", "unstable", "event_relationships"}, map[string]interface{}{
		"event_id":       eventD,
		"room_id":        roomID, // required so the server knows which servers to ask
		"direction":      "down", // no newer events, so nothing should be added
		"include_parent": true,   // this should pull in event B
	})
	var gots []gjson.Result
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyEqual("limited", false),
			match.JSONArrayEach("events", func(r gjson.Result) error {
				gots = append(gots, r)
				return nil
			}),
		},
	})
	if len(gots) != 2 {
		t.Fatalf("/event_relationships got %d events, want 2", len(gots))
	}
	if gots[0].Get("event_id").Str != eventD {
		t.Fatalf("/event_relationships expected first element to be event D but was %s", gots[0].Raw)
	}
	if gots[1].Get("event_id").Str != eventB {
		t.Fatalf("/event_relationships expected second element to be event B but was %s", gots[1].Raw)
	}
	// check the children count of event B to make sure it is 2 (C,D)
	// and check the hash is correct
	checkUnsigned(t, gots[1], map[string]int64{
		"m.reference": 2,
	}, []string{eventC, eventD})

	// now hit /event_relationships again with B, which should return everything (and fetch the missing events A,C)
	res = bob.MustDo(t, "POST", []string{"_matrix", "client", "unstable", "event_relationships"}, map[string]interface{}{
		"event_id":       eventB,
		"room_id":        roomID, // required so the server knows which servers to ask
		"direction":      "down", // this pulls in C,D
		"include_parent": true,   // this pulls in A
		"recent_first":   false,
	})
	gots = []gjson.Result{}
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyEqual("limited", false),
			match.JSONArrayEach("events", func(r gjson.Result) error {
				gots = append(gots, r)
				return nil
			}),
		},
	})
	if len(gots) != 4 {
		t.Fatalf("/event_relationships returned %d events, want 4. %v", len(gots), gots)
	}
	if gots[0].Get("event_id").Str != eventB {
		t.Fatalf("/event_relationships expected first element to be event B but was %s", gots[0].Raw)
	}
	if gots[1].Get("event_id").Str != eventA {
		t.Fatalf("/event_relationships expected second element to be event A but was %s", gots[1].Raw)
	}
	if gots[2].Get("event_id").Str != eventC {
		t.Fatalf("/event_relationships expected third element to be event C but was %s", gots[2].Raw)
	}
	if gots[3].Get("event_id").Str != eventD {
		t.Fatalf("/event_relationships expected fourth element to be event D but was %s", gots[3].Raw)
	}
	// event A has event B as a child
	checkUnsigned(t, gots[1], map[string]int64{
		"m.reference": 1,
	}, []string{eventB})
	// event B has events C,D as children (same as before)
	checkUnsigned(t, gots[0], map[string]int64{
		"m.reference": 2,
	}, []string{eventC, eventD})
}

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
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
	)
	cancel := srv.Listen()
	defer cancel()

	// create a room on Complement, add some events to walk.
	ver := alice.GetDefaultRoomVersion(t)
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
	alice.JoinRoom(t, room.RoomID, []string{srv.ServerName})

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
	fedClient := srv.FederationClient(deployment)
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

func checkUnsigned(t *testing.T, r gjson.Result, wantChildCount map[string]int64, childrenEventIDs []string) {
	t.Helper()
	childCountMap := r.Get(`unsigned.children`)
	if !childCountMap.IsObject() {
		t.Fatalf("checkUnsigned: unsigned.children isn't a map")
	}
	childCount := childCountMap.Map()
	for wantRelType, wantCount := range wantChildCount {
		if childCount[wantRelType].Int() != wantCount {
			t.Errorf("rel_type %s got %d count want %d", wantRelType, childCount[wantRelType].Int(), wantCount)
		}
	}
	// check the hash is correct
	sort.Strings(childrenEventIDs)
	wantHash := sha256.Sum256([]byte(strings.Join(childrenEventIDs, "")))
	gotHash, err := base64.RawStdEncoding.DecodeString(r.Get(`unsigned.children_hash`).Str)
	must.NotError(t, "failed to decode hash as base64", err)
	if !bytes.Equal(gotHash, wantHash[:]) {
		t.Errorf("mismatched children_hash: got %x want %x", gotHash, wantHash[:])
	}
}
