package tests

// The tests in this file all have 2 Synapses and we drive behaviour end-to-end.
// This ensures things work, but means we don't know how they work. For API tests,
// see the other file in this directory.

import (
	"fmt"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/gomatrixserverlib/spec"
)

// Test that you can join and send messages in MSC4242 rooms.
func TestMSC4242FederationSimple(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"room_version": roomVersion,
		"preset":       "public_chat",
	})
	// ensure we are verifying current state by walking the state dag by creating no-op state dag changes.
	changeDisplayName(t, alice, "alice", 5)
	bob.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})
	eventID := bob.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "I work over federation!",
		},
	})
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, eventID))
}

func changeDisplayName(t *testing.T, cli *client.CSAPI, prefix string, numTimes int) {
	for i := 0; i < numTimes; i++ {
		cli.MustSetDisplayName(t, fmt.Sprintf("%s %d", prefix, i))
	}
}

func sendTextMessages(t *testing.T, cli *client.CSAPI, roomID, prefix string, numTimes int) (eventIDs []string) {
	for i := 0; i < numTimes; i++ {
		// we need to send these synced to reduce the chance of self-forking as that would break /get_missing_events assertions.
		eventIDs = append(eventIDs, cli.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]any{
				"msgtype": "m.text",
				"body":    fmt.Sprintf("sendTextMessages %s %d", prefix, i),
			},
		}))
	}
	return
}
