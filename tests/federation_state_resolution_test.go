package tests

import (
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/tidwall/gjson"
)

// Tests state resolution (v2) by forcing a fork with conflicted state, per:
// https://github.com/matrix-org/matrix-spec/issues/1209
// After initial room create and power levels change to enable name setting, we
// have the following:
//
// alice sets name1
//
//	|
//	|---------------------------.
//	|                           |
//	v                           v
//
// alice sets name2       bob kicks alice (this arrives late)
//
//	|                           |
//	|     .---------------------'
//	v     v
//
// what is the current state here
func TestFederationStateResolution(t *testing.T) {
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

	hsName := "hs1"

	alice := deployment.Register(t, hsName, helpers.RegistrationOpts{})
	// Charlie exists so we can view the room after we ban Alice
	charlie := deployment.Register(t, hsName, helpers.RegistrationOpts{})

	bob := srv.UserID("bob")
	ver := alice.GetDefaultRoomVersion(t)
	serverRoom := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, bob))

	getName := func() string {
		res := charlie.GetRoomCurrentState(t, serverRoom.RoomID)
		body, err := io.ReadAll(res.Body)
		if err != nil {
			panic(err)
		}

		var evs []json.RawMessage
		if err := json.Unmarshal(body, &evs); err != nil {
			panic(err)
		}

		for _, ev := range evs {
			if gjson.GetBytes(ev, "type").String() == "m.room.name" {
				return gjson.GetBytes(ev, "content.name").String()
			}
		}
		return ""
	}

	// Join Charlie - this will be a federated join as the server is not yet in the room
	charlie.MustJoinRoom(t, serverRoom.RoomID, []spec.ServerName{srv.ServerName()})
	charlie.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(charlie.UserID, serverRoom.RoomID))
	serverRoom.WaiterForMembershipEvent(charlie.UserID).Waitf(t, 5*time.Second, "failed to see alice join complement server")
	serverRoom.MustHaveMembershipForUser(t, charlie.UserID, "join")

	// Join Alice - this will be a local send since the server is now in the room
	alice.MustJoinRoom(t, serverRoom.RoomID, []spec.ServerName{srv.ServerName()})
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, serverRoom.RoomID))
	serverRoom.WaiterForMembershipEvent(alice.UserID).Waitf(t, 5*time.Second, "failed to see alice join complement server")
	serverRoom.MustHaveMembershipForUser(t, alice.UserID, "join")

	// Send updated power levels so alice can set the room name.
	pwlvlEvt := srv.MustCreateEvent(t, serverRoom, federation.Event{
		Type:     "m.room.power_levels",
		StateKey: b.Ptr(""),
		Sender:   bob,
		Content: map[string]any{
			"m.room.name": 50,
			"users": map[string]any{
				alice.UserID: 50,
				bob:          100, // as before
			},
		},
	})
	serverRoom.AddEvent(pwlvlEvt)
	srv.MustSendTransaction(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, hsName), []json.RawMessage{pwlvlEvt.JSON()}, nil)
	charlie.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(serverRoom.RoomID, pwlvlEvt.EventID()))

	// Bob set an initial name, this will not usually be in the resolved state
	nameEvt := srv.MustCreateEvent(t, serverRoom, federation.Event{
		Type:     "m.room.name",
		StateKey: b.Ptr(""),
		Sender:   bob,
		Content: map[string]any{
			"name": "Bob initial",
		},
	})
	serverRoom.AddEvent(nameEvt)
	srv.MustSendTransaction(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, hsName), []json.RawMessage{nameEvt.JSON()}, nil)
	charlie.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(serverRoom.RoomID, nameEvt.EventID()))
	if getName() != "Bob initial" {
		ct.Fatalf(t, "first initial name not set")
	}

	// Alice now sets the room name, which is allowed. Notably it is this event that will be lost
	// due to the state reset.
	e2 := alice.SendEventSynced(t, serverRoom.RoomID, b.Event{
		Type:     "m.room.name",
		StateKey: b.Ptr(""),
		Content: map[string]any{
			"name": "Alice initial",
		},
	})
	// wait until bob's server sees e
	serverRoom.WaiterForEvent(e2).Waitf(t, 5*time.Second, "failed to see e2 (%s) on complement server", e2)
	if getName() != "Alice initial" {
		ct.Fatalf(t, "second initial name not set")
	}

	// create S2, which kicks alice but don't send it yet
	s2 := srv.MustCreateEvent(t, serverRoom, federation.Event{
		Type:     "m.room.member",
		StateKey: b.Ptr(alice.UserID),
		Sender:   bob,
		Content: map[string]any{
			"membership": "ban",
		},
	})
	serverRoom.AddEvent(s2)

	// Alice now sets the room name agian, which is allowed currently, but should be removed
	e3 := alice.SendEventSynced(t, serverRoom.RoomID, b.Event{
		Type:     "m.room.name",
		StateKey: b.Ptr(""),
		Content: map[string]any{
			"name": "Bad name",
		},
	})
	// wait until bob's server sees e
	serverRoom.WaiterForEvent(e3).Waitf(t, 5*time.Second, "failed to see e3 (%s) on complement server", e3)
	if getName() != "Bad name" {
		ct.Fatalf(t, "First updated name not set")
	}

	// Now send s2, which has prev event e2 same as e3, resulting in two extremeties in the DAG that
	// must be resolved, which should undo e3.
	srv.MustSendTransaction(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, hsName), []json.RawMessage{s2.JSON()}, nil)
	// wait until we see S2 to ensure the server has processed this.
	charlie.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(serverRoom.RoomID, s2.EventID()))

	// Check the name was reverted or dropped. Although this seems counterintuitive the name being
	// dropped is an unfortunate consequence of the state res v2 algorithm, as described here:
	// https://github.com/matrix-org/matrix-spec-proposals/blob/erikj/state_res_msc/proposals/1442-state-resolution.md#state-resets
	if name := getName(); name == "Bad name" {
		ct.Fatalf(t, "initial name not restored after state resolution, got: %s", name)
	} else if name == "" {
		t.Log("original name not restored")
	}
}
