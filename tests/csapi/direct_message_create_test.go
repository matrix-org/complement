package csapi_tests

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
)

// Synapse has a long-standing bug
//     https://github.com/matrix-org/synapse/issues/9768
// where membership events are duplicated in the timeline
// returned by a call to `/sync` with a `since` parameter.
// (AFAICS the spec doesn't mandate that there are no such
// duplicates, but pretty much everyone expects things to
// work that way.)
//
// This test reproduces the duplicated membership event for
// Bob after Alice invites him to a 1-1 room.
func TestMembershipNotDuplicatedWhenJoiningDirectMessage(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	// Alice invites Bob to a room for direct messaging.
	roomID := alice.CreateRoom(t, map[string]interface{}{
		"invite":    []string{bob.UserID},
		"is_direct": true,
	})

	// Bob receives the invite and joins. Extract the
	// `next_batch` from the response for use later.
	nextBatch := bob.SyncUntilInvitedTo(t, roomID)
	bob.JoinRoom(t, roomID, []string{"hs1"})

	// To reproduce the bug, we ought to sync until we see
	// a duplicate membership event. But actually seeing
	// that event should fail the test, so we shouldn't
	// expect it to happen. To account for both cases,
	// send a dummy sentinel event after we've joined.
	SENTINEL_EVENT_TYPE := "com.example.dummy"
	bob.SendEventSynced(t, roomID, b.Event{
		Type:    SENTINEL_EVENT_TYPE,
		Sender:  bob.UserID,
		Content: map[string]interface{}{},
	})

	// Replay a sync from just before we joined until we see
	// the sentinel event. Count the number of Join events we
	// see.
	bobJoinEvents := 0
	bob.SyncUntil(
		t,
		nextBatch,
		"",
		"rooms.join."+client.GjsonEscape(roomID)+".timeline.events",
		func(ev gjson.Result) bool {
			if ev.Get("type").Str == "m.room.member" &&
				ev.Get("state_key").Str == bob.UserID &&
				ev.Get("content.membership").Str == "join" {
				bobJoinEvents += 1
			}

			return ev.Get("type").Str == SENTINEL_EVENT_TYPE
		},
	)

	if bobJoinEvents != 1 {
		t.Fatalf("Saw %d join events for Bob; expected 1", bobJoinEvents)
	}
}
