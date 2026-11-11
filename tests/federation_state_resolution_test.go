package tests

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/gomatrixserverlib/spec"
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
// alice sets name2       bob bans alice (this arrives late)
//
//	|                           |
//	|     .---------------------'
//	v     v
//
// what is the current state here
func TestFederationStateConflictResolution(t *testing.T) {
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
	// Charlie exists so we can view the room after we ban Alice
	charlie := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	bob := srv.UserID("bob")
	ver := alice.GetDefaultRoomVersion(t)
	serverRoom := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, bob))

	// Join Charlie - this will be a federated join as the server is not yet in the room
	charlie.MustJoinRoom(t, serverRoom.RoomID, []spec.ServerName{srv.ServerName()})
	charlie.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(charlie.UserID, serverRoom.RoomID))
	serverRoom.WaiterForStateEvent("m.room.member", charlie.UserID).Waitf(t, 5*time.Second, "failed to see charlie join complement server")
	serverRoom.MustHaveMembershipForUser(t, charlie.UserID, "join")

	// Join Alice - this will be a local send since the server is now in the room
	alice.MustJoinRoom(t, serverRoom.RoomID, []spec.ServerName{srv.ServerName()})
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, serverRoom.RoomID))
	serverRoom.WaiterForStateEvent("m.room.member", alice.UserID).Waitf(t, 5*time.Second, "failed to see alice join complement server")
	serverRoom.MustHaveMembershipForUser(t, alice.UserID, "join")

	// Send updated power levels so alice can set the room name.
	powerlevelEvent := srv.MustCreateEvent(t, serverRoom, federation.Event{
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
	serverRoom.AddEvent(powerlevelEvent)
	srv.MustSendTransaction(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), []json.RawMessage{powerlevelEvent.JSON()}, nil)
	charlie.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(serverRoom.RoomID, powerlevelEvent.EventID()))

	// Bob set an initial name, this will not usually be in the resolved state
	roomNameChangeFromBob := srv.MustCreateEvent(t, serverRoom, federation.Event{
		Type:     "m.room.name",
		StateKey: b.Ptr(""),
		Sender:   bob,
		Content: map[string]any{
			"name": "Bob initial",
		},
	})
	serverRoom.AddEvent(roomNameChangeFromBob)
	srv.MustSendTransaction(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), []json.RawMessage{roomNameChangeFromBob.JSON()}, nil)
	charlie.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(serverRoom.RoomID, roomNameChangeFromBob.EventID()))
	if name := getRoomName(t, charlie, serverRoom.RoomID); name != "Bob initial" {
		ct.Fatalf(t, "expected room name to be %q after roomNameChangeFromBob, got %q", "Bob initial", name)
	}

	// Alice now sets the room name, which is allowed. Notably it is this event that will be lost
	// due to the state reset.
	roomNameChangeFromAlice := alice.SendEventSynced(t, serverRoom.RoomID, b.Event{
		Type:     "m.room.name",
		StateKey: b.Ptr(""),
		Content: map[string]any{
			"name": "Alice initial",
		},
	})
	// wait until bob's server sees the event
	serverRoom.WaiterForEvent(roomNameChangeFromAlice).Waitf(t, 5*time.Second, "failed to see roomNameChangeFromAlice (%s) on complement server", roomNameChangeFromAlice)
	if name := getRoomName(t, charlie, serverRoom.RoomID); name != "Alice initial" {
		ct.Fatalf(t, "expected room name to be %q after roomNameChangeFromAlice, got %q", "Alice initial", name)
	}

	// Create the ban of alice but don't send it yet
	aliceBanEvent := srv.MustCreateEvent(t, serverRoom, federation.Event{
		Type:     "m.room.member",
		StateKey: b.Ptr(alice.UserID),
		Sender:   bob,
		Content: map[string]any{
			"membership": "ban",
		},
	})
	serverRoom.AddEvent(aliceBanEvent)

	// Alice now sets the room name again, which is allowed from hs1's point of view but will be
	// rolled back once hs1 receives aliceBanEvent.
	rolledBackRoomNameChangeFromAlice := alice.SendEventSynced(t, serverRoom.RoomID, b.Event{
		Type:     "m.room.name",
		StateKey: b.Ptr(""),
		Content: map[string]any{
			"name": "another name",
		},
	})
	// wait until bob's server sees the event
	serverRoom.WaiterForEvent(rolledBackRoomNameChangeFromAlice).Waitf(t, 5*time.Second, "failed to see rolledBackRoomNameChangeFromAlice (%s) on complement server", rolledBackRoomNameChangeFromAlice)
	if name := getRoomName(t, charlie, serverRoom.RoomID); name != "another name" {
		ct.Fatalf(t, "expected room name to be %q after rolledBackRoomNameChangeFromAlice, got %q", "another name", name)
	}

	// Now send aliceBanEvent, which has prev event roomNameChangeFromAlice same as
	// rolledBackRoomNameChangeFromAlice, resulting in two extremeties in the DAG that must be resolved.
	srv.MustSendTransaction(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), []json.RawMessage{aliceBanEvent.JSON()}, nil)
	// wait until we see aliceBanEvent to ensure the server has processed this.
	charlie.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(serverRoom.RoomID, aliceBanEvent.EventID()))

	// The ban is a power event so is resolved first, after which both of alice's name changes fail
	// auth and the name is dropped entirely rather than restored to "Alice initial". Although this
	// seems counterintuitive it is an unfortunate consequence of the state res v2 algorithm, as described here:
	// https://github.com/matrix-org/matrix-spec-proposals/blob/erikj/state_res_msc/proposals/1442-state-resolution.md#state-resets
	if name := getRoomName(t, charlie, serverRoom.RoomID); name != "" {
		ct.Fatalf(t, "expected room name to be removed by state resolution, got %q", name)
	}
}

func getRoomName(t *testing.T, c *client.CSAPI, roomID string) string {
	t.Helper()
	res := c.GetRoomCurrentState(t, roomID)
	for _, ev := range must.ParseJSON(t, res.Body).Array() {
		if ev.Get("type").Str == "m.room.name" {
			return ev.Get("content.name").Str
		}
	}
	return ""
}
