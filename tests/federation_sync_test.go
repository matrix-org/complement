package tests

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/tidwall/gjson"
)

// Tests https://github.com/element-hq/synapse/issues/16928
// To set up:
// - Alice and Bob join the same room, sends E1.
// - Alice sends event E3.
// - Bob forks the graph at E1 and sends S2.
// - Alice sends event E4 on the same fork as E3.
// - Alice sends event E5 merging the forks.
// - Alice sync with timeline_limit=1 and a filter that skips E5
func TestSyncOmitsStateChangeOnFilteredEvents(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(nil, nil),
	)
	srv.UnexpectedRequestsAreErrors = false
	cancel := srv.Listen()
	defer cancel()

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := srv.UserID("bob")
	ver := alice.GetDefaultRoomVersion(t)
	serverRoom := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, bob))

	// Join Alice to the new room on the federation server and send E1.
	alice.MustJoinRoom(t, serverRoom.RoomID, []string{srv.ServerName()})
	e1 := alice.SendEventSynced(t, serverRoom.RoomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "E1",
		},
	})

	// wait until bob's server sees e1
	serverRoom.WaiterForEvent(e1).Waitf(t, 5*time.Second, "failed to see e1 (%s) on complement server", e1)

	// create S2 but don't send it yet, prev_events will be set to [e1]
	roomName := "I am the room name, S2"
	s2 := srv.MustCreateEvent(t, serverRoom, federation.Event{
		Type:     "m.room.name",
		StateKey: b.Ptr(""),
		Sender:   bob,
		Content: map[string]interface{}{
			"name": roomName,
		},
	})

	// Alice sends E3 & E4
	alice.SendEventSynced(t, serverRoom.RoomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "E3",
		},
	})
	alice.SendEventSynced(t, serverRoom.RoomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "E4",
		},
	})

	// fork the dag earlier at e1 and send s2
	srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{s2.JSON()}, nil)

	// wait until we see S2 to ensure the server has processed this.
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(serverRoom.RoomID, s2.EventID()))

	// now send E5, merging the forks
	alice.SendEventSynced(t, serverRoom.RoomID, b.Event{
		Type: "please_filter_me",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "E5",
		},
	})

	// now do a sync request which filters events of type 'please_filter_me', and ensure we still see the
	// correct room name. Note we don't need to SyncUntil here as we have all the data in the right places
	// already.
	res, _ := alice.MustSync(t, client.SyncReq{
		Filter: `{
			"room": {
				"timeline": {
					"not_types": ["please_filter_me"],
					"limit": 1
				}
			}
		}`,
	})
	must.MatchGJSON(t, res, match.JSONCheckOffAllowUnwanted(
		// look in this array
		fmt.Sprintf("rooms.join.%s.state.events", client.GjsonEscape(serverRoom.RoomID)),
		// for these items
		[]interface{}{s2.EventID()},
		// and map them first into this format
		func(r gjson.Result) interface{} {
			return r.Get("event_id").Str
		}, nil,
	))
}
