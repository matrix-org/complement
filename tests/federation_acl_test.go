package tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
	"github.com/matrix-org/complement/should"
)

// Test for https://github.com/matrix-org/dendrite/issues/3004
func TestACLs(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // needs https://github.com/matrix-org/dendrite/pull/3008
	// 1. Prepare 3 or more servers. 1st will be room host, 2nd will be blocked with m.room.server_acl and 3rd server will be affected by this issue. 1st and 2nd servers don't have to be powered by dendrite.
	deployment := complement.Deploy(t, 3)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})
	charlie := deployment.Register(t, "hs3", helpers.RegistrationOpts{})

	// 2. Create room on 1st server
	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	aliceSince := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

	// 3. Join this room from 2nd server
	bob.MustJoinRoom(t, roomID, []string{"hs1"})
	aliceSince = alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncJoinedTo(bob.UserID, roomID))
	bobSince := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	// create a different room used for a sentinel event
	sentinelRoom := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	aliceSince = alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncJoinedTo(alice.UserID, sentinelRoom))
	bob.MustJoinRoom(t, sentinelRoom, []string{"hs1"})
	charlie.MustJoinRoom(t, sentinelRoom, []string{"hs1"})
	aliceSince = alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince},
		client.SyncJoinedTo(bob.UserID, sentinelRoom),
		client.SyncJoinedTo(charlie.UserID, sentinelRoom),
	)

	// 4. Add deny rule, to block 2nd server from participating
	stateKey := ""
	eventID := alice.SendEventSynced(t, roomID, b.Event{
		Type:     "m.room.server_acl",
		Sender:   alice.UserID,
		StateKey: &stateKey,
		Content: map[string]interface{}{
			"allow":             []string{"*"},
			"allow_ip_literals": true,
			"deny":              []string{"hs2"},
		},
	})
	// wait for the ACL to show up on hs2
	bob.MustSyncUntil(t, client.SyncReq{Since: bobSince}, client.SyncTimelineHasEventID(roomID, eventID))

	// 5. Join from 3rd server.
	charlie.MustJoinRoom(t, roomID, []string{"hs1"})
	aliceSince = alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncJoinedTo(charlie.UserID, roomID))
	charlieSince := charlie.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(charlie.UserID, roomID))

	// 6. Any events sent by 2nd server will not appear for users from 1st server, but will be visible for users from 3rd server
	eventID = bob.SendEventSynced(t, roomID, b.Event{
		Type:   "m.room.message",
		Sender: bob.UserID,
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "I should be blocked",
		},
	})

	// sentinel event in room2
	sentinelEventID := bob.SendEventSynced(t, sentinelRoom, b.Event{
		Type:   "m.room.message",
		Sender: bob.UserID,
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "I should be visible",
		},
	})
	// wait for the sentinel event to come down sync
	alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncTimelineHasEventID(sentinelRoom, sentinelEventID))
	charlie.MustSyncUntil(t, client.SyncReq{Since: charlieSince}, client.SyncTimelineHasEventID(sentinelRoom, sentinelEventID))

	// Verify with alice and charlie that we never received eventID
	for _, user := range []*client.CSAPI{alice, charlie} {
		syncResp, _ := user.MustSync(t, client.SyncReq{})

		// we don't expect eventID (blocked) to be in the sync response
		events := should.GetTimelineEventIDs(syncResp, roomID)
		must.NotContainSubset(t, events, []string{eventID})

		// also check that our sentinel event is present
		events = should.GetTimelineEventIDs(syncResp, sentinelRoom)
		must.ContainSubset(t, events, []string{sentinelEventID})

		// Validate the ACL event is actually in the rooms state
		content := user.MustGetStateEventContent(t, roomID, "m.room.server_acl", "")
		must.MatchGJSON(t, content,
			match.JSONKeyEqual("allow", []string{"*"}),
			match.JSONKeyEqual("deny", []string{"hs2"}),
			match.JSONKeyEqual("allow_ip_literals", true),
		)
	}
}
