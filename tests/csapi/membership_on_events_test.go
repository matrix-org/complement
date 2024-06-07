package csapi_tests

import (
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/runtime"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/helpers"
)

// Membership information on events served to clients, as specified in MSC4115.
//
// Alice sends one message before Vob joins, then one after. Bob reads both messages, and checks the membership state
// on each.
func TestMembershipOnEvents(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // not yet implemented

	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	preJoinEventID := alice.SendEventSynced(t, roomID, b.Event{Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "prejoin",
		}})
	bob.MustJoinRoom(t, roomID, []string{"hs1"})
	postJoinEventID := alice.SendEventSynced(t, roomID, b.Event{Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "postjoin",
		}})

	// Now Bob syncs, to get the messages
	syncResult, _ := bob.MustSync(t, client.SyncReq{})
	if err := client.SyncTimelineHasEventID(roomID, preJoinEventID)(alice.UserID, syncResult); err != nil {
		t.Fatalf("Sync response lacks prejoin event: %s", err)
	}
	if err := client.SyncTimelineHasEventID(roomID, postJoinEventID)(alice.UserID, syncResult); err != nil {
		t.Fatalf("Sync response lacks prejoin event: %s", err)
	}

	// ... and we check the membership value for each event. Should be "leave" for each event until the join.
	haveSeenJoin := false
	roomSyncResult := syncResult.Get("rooms.join." + client.GjsonEscape(roomID))
	for _, ev := range roomSyncResult.Get("timeline.events").Array() {
		if ev.Get("type").Str == "m.room.member" && ev.Get("state_key").Str == bob.UserID {
			haveSeenJoin = true
		}
		membership := ev.Get("unsigned.membership").Str
		expectedMembership := "leave"
		if haveSeenJoin {
			expectedMembership = "join"
		}
		if membership != expectedMembership {
			t.Errorf("Incorrect membership for event %s; got %s, want %s", ev, membership, expectedMembership)
		}
	}
}
