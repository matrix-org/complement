// +buaaild msc2836

package tests

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/tidwall/gjson"
)

// This test checks that federated threading works

// This test checks that the homeserver makes a federated request to /event_relationships
// when walking a thread when it encounters an unknown event ID.
func TestFederatedEventRelationships(t *testing.T) {
	deployment := Deploy(t, "msc2836_fed", b.BlueprintAlice)
	defer deployment.Destroy(t)
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleEventRequests(),
	)
	cancel := srv.Listen()
	defer cancel()

	// create a room on Complement, add some events to walk.
	//     A
	//    / \
	//   B   C
	//       |
	//       D
	roomVer := gomatrixserverlib.RoomVersionV6
	charlie := srv.UserID("charlie")
	room := srv.MustMakeRoom(t, roomVer, federation.InitialRoomEvents(roomVer, charlie))
	root := srv.MustCreateEvent(t, room, b.Event{
		Type:   "m.room.message",
		Sender: charlie,
		Content: map[string]interface{}{
			"body":    "[A] First message",
			"msgtype": "m.text",
		},
	})
	room.AddEvent(root)
	room.AddEvent(srv.MustCreateEvent(t, room, b.Event{
		Type:   "m.room.message",
		Sender: charlie,
		Content: map[string]interface{}{
			"body":    "[B] Top level reply 1",
			"msgtype": "m.text",
			"m.relationship": map[string]interface{}{
				"rel_type": "m.reference",
				"event_id": root.EventID(),
			},
		},
	}))
	topLevelReply := srv.MustCreateEvent(t, room, b.Event{
		Type:   "m.room.message",
		Sender: charlie,
		Content: map[string]interface{}{
			"body":    "[C] Top level reply 2",
			"msgtype": "m.text",
			"m.relationship": map[string]interface{}{
				"rel_type": "m.reference",
				"event_id": root.EventID(),
			},
		},
	})
	room.AddEvent(topLevelReply)
	sndLevelReply := srv.MustCreateEvent(t, room, b.Event{
		Type:   "m.room.message",
		Sender: charlie,
		Content: map[string]interface{}{
			"body":    "[D] Second level reply",
			"msgtype": "m.text",
			"m.relationship": map[string]interface{}{
				"rel_type": "m.reference",
				"event_id": topLevelReply.EventID(),
			},
		},
	})
	room.AddEvent(sndLevelReply)

	// join the room on HS1
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	alice.JoinRoom(t, room.RoomID)

	// send a new child in the thread (child of D)
	trigger := srv.MustCreateEvent(t, room, b.Event{
		Type:   "m.room.message",
		Sender: charlie,
		Content: map[string]interface{}{
			"body":    "[E] Third level reply",
			"msgtype": "m.text",
			"m.relationship": map[string]interface{}{
				"rel_type": "m.reference",
				"event_id": sndLevelReply.EventID(),
			},
		},
	})
	room.AddEvent(trigger)
	fedClient := srv.FederationClient(deployment, "hs1")
	_, err := fedClient.SendTransaction(context.Background(), gomatrixserverlib.Transaction{
		TransactionID:  "complement",
		Origin:         gomatrixserverlib.ServerName(srv.ServerName),
		Destination:    gomatrixserverlib.ServerName("hs1"),
		OriginServerTS: gomatrixserverlib.AsTimestamp(time.Now()),
		PDUs: []json.RawMessage{
			trigger.JSON(),
		},
	})
	must.NotError(t, "failed to SendTransaction", err)

	// Hit /event_relationships to make sure it spiders the whole thing by asking /event_relationships on Complement
	res := alice.MustDo(t, "POST", []string{"_matrix", "client", "unstable", "event_relationships"}, map[string]interface{}{
		"event_id":       trigger.EventID(),
		"max_depth":      10,
		"include_parent": true,
	})
	var events []gomatrixserverlib.Event
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyEqual("limited", false),
			match.JSONArrayEach("events", func(r gjson.Result) error {
				ev, err := gomatrixserverlib.NewEventFromTrustedJSON([]byte(r.Raw), false, roomVer)
				if err != nil {
					return err
				}
				events = append(events, ev)
				return nil
			}),
		},
	})
	for _, ev := range events {
		t.Logf("event: %s", string(ev.JSON()))
	}
	//t.Fatalf("nah")
}
