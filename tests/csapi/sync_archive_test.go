package csapi_tests

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/runtime"
)

// sytest: Left rooms appear in the leave section of sync
func TestSyncLeaveSection(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/1323
	t.Parallel()

	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	includeLeaveFilter := createFilter(t, alice, map[string]interface{}{
		"room": map[string]interface{}{
			"include_leave": true,
		},
	})

	roomID := alice.CreateRoom(t, map[string]interface{}{})

	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

	s := func(req client.SyncReq) string {
		req.TimeoutMillis = "0"
		_, token := alice.MustSync(t, req)
		return token
	}

	// Get independently-fetched sync tokens for every subsequent test,
	// to isolate interference/side effects otherwise possibly caused by
	// multiple sync calls reusing the same token with different filters or
	// settings.
	fullStateSince := s(client.SyncReq{
		Filter: includeLeaveFilter,
	})
	incrementalSince := s(client.SyncReq{})

	alice.LeaveRoom(t, roomID)

	// sytest: Left rooms appear in the leave section of sync
	t.Run("Left rooms appear in the leave section of sync", func(t *testing.T) {
		// SyncLeftFrom does "active" probing of rooms.leave if userID == clientUserID
		alice.MustSyncUntil(t, client.SyncReq{
			Filter: includeLeaveFilter,
		}, client.SyncLeftFrom(alice.UserID, roomID))
	})

	// sytest: Left rooms appear in the leave section of full state sync
	t.Run("Left rooms appear in the leave section of full state sync", func(t *testing.T) {
		alice.MustSyncUntil(t, client.SyncReq{
			Since:     fullStateSince,
			Filter:    includeLeaveFilter,
			FullState: true,
		}, client.SyncLeftFrom(alice.UserID, roomID))
	})

	// sytest: Newly left rooms appear in the leave section of incremental sync
	t.Run("Newly left rooms appear in the leave section of incremental sync", func(t *testing.T) {
		alice.MustSyncUntil(t, client.SyncReq{
			Since: incrementalSince,
		}, client.SyncLeftFrom(alice.UserID, roomID))
	})
}

// sytest: Newly left rooms appear in the leave section of gapped sync
func TestGappedSyncLeaveSection(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/1323
	t.Parallel()

	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	gappyFilter := createFilter(t, alice, map[string]interface{}{
		"room": map[string]interface{}{
			"timeline": map[string]int64{
				"limit": 1,
			},
			"include_leave": true,
		},
	})

	roomToLeave := alice.CreateRoom(t, map[string]interface{}{})
	// j0j0: The original sytest creates an additional room to send events into,
	//  my only suspicion as to why is to trigger a gapped-sync bug,
	//  to see if it "skips" over the leave event.
	roomToSpam := alice.CreateRoom(t, map[string]interface{}{})

	_, sinceToken := alice.MustSync(t, client.SyncReq{Filter: gappyFilter, TimeoutMillis: "0"})

	alice.LeaveRoom(t, roomToLeave)

	for i := 0; i < 20; i++ {
		alice.SendEventSynced(t, roomToSpam, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "Mr Anderson...",
			},
		})
	}

	alice.MustSyncUntil(t, client.SyncReq{Filter: gappyFilter, Since: sinceToken}, client.SyncLeftFrom(alice.UserID, roomToLeave))
}

// sytest: Archived rooms only contain history from before the user left
func TestArchivedRoomsHistory(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/1323
	t.Parallel()

	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	const madeUpTestStateType = "a.madeup.test.state"

	filter := map[string]interface{}{
		"room": map[string]interface{}{
			"timeline": map[string][]string{
				"types": {
					"m.room.message",
					madeUpTestStateType,
				},
			},
			"state": map[string][]string{
				"types": {
					madeUpTestStateType,
				},
			},
			"include_leave": true,
		},
	}

	//aliceFilter := createFilter(t, alice, filter)
	bobFilter := createFilter(t, bob, filter)

	roomID := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	bob.JoinRoom(t, roomID, nil)
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	_, bobSince := bob.MustSync(t, client.SyncReq{Filter: bobFilter, TimeoutMillis: "0"})

	alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "before",
		},
	})

	alice.SendEventSynced(t, roomID, b.Event{
		Type:     madeUpTestStateType,
		StateKey: b.Ptr(""),
		Content: map[string]interface{}{
			"my_key": "before",
		},
	})

	bob.LeaveRoom(t, roomID)
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(bob.UserID, roomID))

	alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "after",
		},
	})

	alice.SendEventSynced(t, roomID, b.Event{
		Type:     madeUpTestStateType,
		StateKey: b.Ptr(""),
		Content: map[string]interface{}{
			"my_key": "after",
		},
	})

	exhaustiveResCheck := func(result gjson.Result, origin string) {
		roomRes := result.Get("rooms.leave." + client.GjsonEscape(roomID))

		stateEvents := roomRes.Get("state.events")
		if stateEvents.Exists() {
			if !stateEvents.IsArray() {
				t.Fatalf("state.events is not an array")
			}
			if len(stateEvents.Array()) != 0 {
				t.Fatalf("expected no state events in %s", origin)
			}
		}

		timelineEvents := roomRes.Get("timeline.events")
		if !timelineEvents.Exists() {
			t.Fatalf("timeline.events does not exist in %s", origin)
		}
		if !timelineEvents.IsArray() {
			t.Fatalf("timeline.events is not an array")
		}
		if len(timelineEvents.Array()) != 2 {
			t.Fatalf("Expected two timeline events in %s", origin)
		}

		timelineEvent := timelineEvents.Array()[0]
		if timelineEvent.Get("content.body").Str != "before" {
			t.Fatalf("Expected only events from before leaving in %s", origin)
		}
	}

	syncRes, _ := bob.MustSync(t, client.SyncReq{Filter: bobFilter})

	exhaustiveResCheck(syncRes, "syncRes")

	sinceSyncRes, _ := bob.MustSync(t, client.SyncReq{Filter: bobFilter, Since: bobSince})

	exhaustiveResCheck(sinceSyncRes, "sinceSyncRes")
}

// This tests if rooms in the leave section get removed after a sync.
//
// sytest: Previously left rooms don't appear in the leave section of sync
func TestOlderLeftRoomsNotInLeaveSection(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/1323
	t.Parallel()

	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	aliceFilter := createFilter(t, alice, map[string]interface{}{
		"room": map[string]interface{}{
			"timeline": map[string]int64{
				"limit": 1,
			},
			"include_leave": true,
		},
	})

	roomToLeave := alice.CreateRoom(t, map[string]string{
		"preset": "public_chat",
	})
	roomToSpam := alice.CreateRoom(t, map[string]string{
		"preset": "public_chat",
	})

	bob.JoinRoom(t, roomToLeave, nil)
	bob.JoinRoom(t, roomToSpam, nil)
	bob.MustSyncUntil(
		t,
		client.SyncReq{},
		client.SyncJoinedTo(bob.UserID, roomToLeave),
		client.SyncJoinedTo(bob.UserID, roomToSpam),
	)

	_, aliceSince := alice.MustSync(t, client.SyncReq{Filter: aliceFilter, TimeoutMillis: "0"})

	alice.LeaveRoom(t, roomToLeave)

	aliceSince = alice.MustSyncUntil(t, client.SyncReq{Filter: aliceFilter, Since: aliceSince}, client.SyncLeftFrom(alice.UserID, roomToLeave))

	bob.SendEventSynced(t, roomToLeave, b.Event{
		Type:     "m.room.member",
		StateKey: b.Ptr(bob.UserID),
		Content: map[string]interface{}{
			"membership": "join",
			"filler":     "Welcome back, my friends, to the show that never ends...",
		},
	})

	// Pad out the timeline with filler messages to create a "gap" between
	// this sync and the next.
	for i := 0; i < 20; i++ {
		bob.SendEventSynced(t, roomToSpam, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "Mr Anderson...",
			},
		})
	}

	syncRes, _ := alice.MustSync(t, client.SyncReq{Filter: aliceFilter, Since: aliceSince})

	roomLeaveRes := syncRes.Get("rooms.leave")
	if roomLeaveRes.Exists() {
		if !roomLeaveRes.IsObject() {
			t.Fatalf("rooms.leave is not an object in syncRes")
		}

		if len(roomLeaveRes.Map()) != 0 {
			t.Fatalf("Expected no rooms in 'leave' state")
		}
	}
}

// sytest: We should see our own leave event, even if history_visibility is
//  restricted (SYN-662)
func TestLeaveEventVisibility(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/1323
	t.Parallel()

	// Note: this deviates from the sytest by introducing a second user,
	//  this user is only meant to keep the room alive,
	//  as a room with no users may be purged by the server,
	//  creating side effects that this test is not looking for.
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	aliceFilter := createFilter(t, alice, map[string]interface{}{
		"room": map[string]interface{}{
			"include_leave": true,
		},
	})

	roomID := alice.CreateRoom(t, map[string]interface{}{
		"initial_state": []map[string]interface{}{
			{
				"content": map[string]interface{}{
					"history_visibility": "joined",
				},
				"type":      "m.room.history_visibility",
				"state_key": "",
			},
		},
		"preset": "public_chat",
	})
	bob.JoinRoom(t, roomID, nil)

	aliceSince := alice.MustSyncUntil(
		t,
		client.SyncReq{Filter: aliceFilter},
		client.SyncJoinedTo(alice.UserID, roomID),
		client.SyncJoinedTo(bob.UserID, roomID),
	)

	alice.LeaveRoom(t, roomID)
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(alice.UserID, roomID))

	syncRes, _ := alice.MustSync(t, client.SyncReq{Filter: aliceFilter, Since: aliceSince, TimeoutMillis: "0"})

	roomRes := syncRes.Get("rooms.leave." + client.GjsonEscape(roomID))

	if !roomRes.Exists() {
		t.Fatalf("could not find %s in rooms.leave", roomID)
	}

	stateResult := roomRes.Get("state.events")

	if stateResult.Exists() {
		if !stateResult.IsArray() {
			t.Fatalf("state.events was not an array")
		} else {
			if len(stateResult.Array()) != 0 {
				t.Fatalf("Expected no state events")
			}
		}
	}

	timelineRes := roomRes.Get("timeline.events")

	if !timelineRes.Exists() {
		t.Fatalf("could not find timeline.events")
	} else if !timelineRes.IsArray() {
		t.Fatalf("timeline.events was not an array")
	} else if len(timelineRes.Array()) != 1 {
		t.Fatalf("expected exactly 1 item in timeline")
	}

	event := timelineRes.Array()[0]

	if event.Get("type").Str != "m.room.member" {
		t.Fatalf("expected timeline event was not m.room.member")
	} else if event.Get("sender").Str != alice.UserID {
		t.Fatalf("sender of membership event was not alice")
	} else if event.Get("content.membership").Str != "leave" {
		t.Fatalf("membership event was not leave")
	}
}

// sytest: We should see our own leave event when rejecting an invite,
//  even if history_visibility is restricted (riot-web/3462)
func TestLeaveEventInviteRejection(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/1323
	t.Parallel()

	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	aliceFilter := createFilter(t, alice, map[string]interface{}{
		"room": map[string]interface{}{
			"include_leave": true,
		},
	})

	roomID := bob.CreateRoom(t, map[string]interface{}{
		"initial_state": []map[string]interface{}{
			{
				"content": map[string]interface{}{
					"history_visibility": "joined",
				},
				"type":      "m.room.history_visibility",
				"state_key": "",
			},
		},
	})

	_, aliceSince := alice.MustSync(t, client.SyncReq{TimeoutMillis: "0", Filter: aliceFilter})

	bob.InviteRoom(t, roomID, alice.UserID)

	aliceSince = alice.MustSyncUntil(
		t,
		client.SyncReq{Filter: aliceFilter, Since: aliceSince},
		client.SyncInvitedTo(alice.UserID, roomID),
	)

	alice.LeaveRoom(t, roomID)

	aliceSince = alice.MustSyncUntil(
		t,
		client.SyncReq{Filter: aliceFilter, Since: aliceSince},
		client.SyncLeftFrom(alice.UserID, roomID),
	)
}
