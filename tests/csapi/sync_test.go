package csapi_tests

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/runtime"
)

// Observes "first bug" from https://github.com/matrix-org/dendrite/pull/1394#issuecomment-687056673
func TestCumulativeJoinLeaveJoinSync(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := bob.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	var since string

	// Get floating next_batch from before joining at all
	_, since = alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

	alice.JoinRoom(t, roomID, nil)

	// This assumes that sync does not have side-effects in servers.
	//
	// The alternative would be to sleep, but that is not acceptable here.
	sinceJoin := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

	alice.LeaveRoom(t, roomID)

	sinceLeave := alice.MustSyncUntil(t, client.SyncReq{Since: sinceJoin}, client.SyncLeftFrom(alice.UserID, roomID))

	alice.JoinRoom(t, roomID, nil)

	alice.MustSyncUntil(t, client.SyncReq{Since: sinceLeave}, client.SyncJoinedTo(alice.UserID, roomID))

	jsonRes, _ := alice.MustSync(t, client.SyncReq{TimeoutMillis: "0", Since: since})
	if jsonRes.Get("rooms.leave." + client.GjsonEscape(roomID)).Exists() {
		t.Errorf("Incremental sync has joined-left-joined room showing up in leave section, this shouldnt be the case.")
	}
}

// Observes "second bug" from https://github.com/matrix-org/dendrite/pull/1394#issuecomment-687056673
func TestTentativeEventualJoiningAfterRejecting(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	var since string
	var jsonRes gjson.Result

	// Get floating current next_batch
	_, since = alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

	alice.InviteRoom(t, roomID, bob.UserID)

	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))

	// This rejects the invite
	bob.LeaveRoom(t, roomID)

	// Full sync
	leaveExists := false
	start := time.Now()
	for !leaveExists && time.Since(start) < 1*time.Second {
		jsonRes, since = bob.MustSync(t, client.SyncReq{TimeoutMillis: "0", FullState: true, Since: since})
		leaveExists = jsonRes.Get("rooms.leave." + client.GjsonEscape(roomID)).Exists()
	}
	if !leaveExists {
		t.Errorf("Bob just rejected an invite, it should show up under 'leave' in a full sync")
	}

	bob.JoinRoom(t, roomID, nil)

	start = time.Now()
	leaveExists = true
	for leaveExists && time.Since(start) < 1*time.Second {
		jsonRes, since = bob.MustSync(t, client.SyncReq{TimeoutMillis: "0", FullState: true, Since: since})
		leaveExists = jsonRes.Get("rooms.leave." + client.GjsonEscape(roomID)).Exists()
	}
	if leaveExists {
		t.Errorf("Bob has rejected an invite, but then just joined the public room anyways, it should not show up under 'leave' in a full sync %s", since)
	}
}

func TestSync(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // too flakey, fails with sync_test.go:135: unchanged room !7ciB69Jg2lCc4Vdf:hs1 should not be in the sync
	// sytest: Can sync
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	filter := map[string]interface{}{
		"room": map[string]interface{}{
			"timeline": map[string]interface{}{
				"limit": 10,
			},
		},
	}
	f, err := json.Marshal(filter)
	if err != nil {
		t.Errorf("unable to marshal filter: %v", err)
	}
	filterID := createFilter(t, alice, f, alice.UserID)

	t.Run("parallel", func(t *testing.T) {
		// sytest: Can sync a joined room
		t.Run("Can sync a joined room", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, struct{}{})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
			res, nextBatch := alice.MustSync(t, client.SyncReq{Filter: filterID})
			// check all required fields exist
			checkJoinFieldsExist(t, res, roomID)
			// sync again
			res, _ = alice.MustSync(t, client.SyncReq{Filter: filterID, Since: nextBatch})
			if res.Get("rooms.join." + client.GjsonEscape(roomID)).Exists() {
				t.Errorf("unchanged room %s should not be in the sync", roomID)
			}
		})
		// sytest: Full state sync includes joined rooms
		t.Run("Full state sync includes joined rooms", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, struct{}{})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
			_, nextBatch := alice.MustSync(t, client.SyncReq{Filter: filterID})

			res, _ := alice.MustSync(t, client.SyncReq{Filter: filterID, Since: nextBatch, FullState: true})
			checkJoinFieldsExist(t, res, roomID)
		})
		// sytest: Newly joined room is included in an incremental sync
		t.Run("Newly joined room is included in an incremental sync", func(t *testing.T) {
			t.Parallel()
			_, nextBatch := alice.MustSync(t, client.SyncReq{Filter: filterID})
			roomID := alice.CreateRoom(t, struct{}{})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
			res, nextBatch := alice.MustSync(t, client.SyncReq{Filter: filterID, Since: nextBatch})
			checkJoinFieldsExist(t, res, roomID)
			res, _ = alice.MustSync(t, client.SyncReq{Filter: filterID, Since: nextBatch})
			if res.Get("rooms.join." + client.GjsonEscape(roomID)).Exists() {
				t.Errorf("unchanged room %s should not be in the sync", roomID)
			}
		})
		// sytest: Newly joined room has correct timeline in incremental sync
		t.Run("Newly joined room has correct timeline in incremental sync", func(t *testing.T) {
			runtime.SkipIf(t, runtime.Dendrite) // does not yet pass
			t.Parallel()
			filter = map[string]interface{}{
				"room": map[string]interface{}{
					"timeline": map[string]interface{}{
						"limit": 10,
						"types": []string{"m.room.message"},
					},
					"state": map[string]interface{}{
						"types": []string{},
					},
				},
			}
			f, err = json.Marshal(filter)
			if err != nil {
				t.Errorf("unable to marshal filter: %v", err)
			}
			filterBob := createFilter(t, bob, f, bob.UserID)

			roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

			sendMessages(t, alice, roomID, "alice message 1-", 4)
			_, nextBatch := bob.MustSync(t, client.SyncReq{Filter: filterBob})
			sendMessages(t, alice, roomID, "alice message 2-", 4)
			bob.JoinRoom(t, roomID, []string{})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
			res, _ := bob.MustSync(t, client.SyncReq{Filter: filterBob, Since: nextBatch})
			room := res.Get("rooms.join." + client.GjsonEscape(roomID))
			timeline := room.Get("timeline")
			limited := timeline.Get("limited").Bool()
			timelineEvents := timeline.Get("events").Array()
			for _, event := range timelineEvents {
				if event.Get("type").Str != "m.room.message" {
					t.Errorf("Only expected 'm.room.message' events")
				}
			}
			if len(timelineEvents) == 6 {
				if limited {
					t.Errorf("Timeline has all the events so shouldn't be limited: %+v", timeline)
				}
			} else {
				if !limited {
					t.Errorf("Timeline doesn't have all the events so should be limited: %+v", timeline)
				}
			}
		})
		// sytest: Newly joined room includes presence in incremental sync
		t.Run("Newly joined room includes presence in incremental sync", func(t *testing.T) {
			runtime.SkipIf(t, runtime.Dendrite) // does not yet pass
			roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
			_, nextBatch := bob.MustSync(t, client.SyncReq{})
			bob.JoinRoom(t, roomID, []string{})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
			nextBatch = bob.MustSyncUntil(t, client.SyncReq{Since: nextBatch}, func(userID string, sync gjson.Result) error {
				presence := sync.Get("presence")
				if len(presence.Get("events").Array()) == 0 {
					return fmt.Errorf("presence.events is empty: %+v", presence)
				}
				usersInPresenceEvents(t, presence, []string{alice.UserID})
				return nil
			})
			// There should be no new presence events
			res, _ := bob.MustSync(t, client.SyncReq{Since: nextBatch})
			usersInPresenceEvents(t, res.Get("presence"), []string{})
		})
		// sytest: Get presence for newly joined members in incremental sync
		t.Run("Get presence for newly joined members in incremental sync", func(t *testing.T) {
			runtime.SkipIf(t, runtime.Dendrite) // does not yet pass
			roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})
			nextBatch := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
			sendMessages(t, alice, roomID, "dummy message", 1)
			_, nextBatch = alice.MustSync(t, client.SyncReq{Since: nextBatch})
			bob.JoinRoom(t, roomID, []string{})
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

			// wait until there are presence events
			nextBatch = alice.MustSyncUntil(t, client.SyncReq{Since: nextBatch}, func(userID string, sync gjson.Result) error {
				presence := sync.Get("presence")
				if len(presence.Get("events").Array()) == 0 {
					return fmt.Errorf("presence.events is empty: %+v", presence)
				}
				usersInPresenceEvents(t, presence, []string{bob.UserID})
				return nil
			})
			// There should be no new presence events
			res, _ := alice.MustSync(t, client.SyncReq{Since: nextBatch})
			usersInPresenceEvents(t, res.Get("presence"), []string{})
		})
	})
}

func sendMessages(t *testing.T, client *client.CSAPI, roomID string, prefix string, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		client.SendEventSynced(t, roomID, b.Event{
			Sender: client.UserID,
			Type:   "m.room.message",
			Content: map[string]interface{}{
				"body":    fmt.Sprintf("%s%d", prefix, i),
				"msgtype": "m.text",
			},
		})
	}
}

func checkJoinFieldsExist(t *testing.T, res gjson.Result, roomID string) {
	t.Helper()
	room := res.Get("rooms.join." + client.GjsonEscape(roomID))
	timeline := room.Get("timeline")
	if !timeline.Exists() {
		t.Errorf("timeline does not exist: %+v", res)
	}
	for _, x := range []string{"events", "limited", "prev_batch"} {
		if !timeline.Get(x).Exists() {
			t.Errorf("timeline %s does not exist", x)
		}
	}
	state := room.Get("state")
	if !state.Exists() {
		t.Errorf("state does not exist: %+v", res)
	}
	if !state.Get("events").Exists() {
		t.Errorf("state events do not exist")
	}
	if !state.Get("events").IsArray() {
		t.Errorf("state events is not an array")
	}
	ephemeral := room.Get("ephemeral")
	if !ephemeral.Exists() {
		t.Errorf("ephemeral does not exist: %+v", res)
	}
	if !ephemeral.Get("events").Exists() {
		t.Errorf("ephemeral events do not exist")
	}
	if !ephemeral.Get("events").IsArray() {
		t.Errorf("ephemeral events is not an array")
	}
}

// usersInPresenceEvents checks that all users are present in presence.events. If the users list is empty,
// it is expected that presence.events is empty as well. Also verifies that all needed fields are present.
func usersInPresenceEvents(t *testing.T, presence gjson.Result, users []string) {
	t.Helper()
	if users == nil {
		t.Fatal("can not use nil as string slice")
	}

	presenceEvents := presence.Get("events").Array()

	if len(users) > len(presenceEvents) {
		t.Fatalf("expected at least %d presence events, got %d", len(users), len(presenceEvents))
	}

	foundCounter := 0
	for i := range users {
		for _, x := range presenceEvents {
			if x.Get("sender").Str == users[i] {
				foundCounter++
			}
			ok := x.Get("type").Exists() && x.Get("sender").Exists() && x.Get("content").Exists()
			if !ok {
				t.Fatalf("missing field for presence event:")
			}
			if x.Get("type").Str != "m.presence" {
				t.Fatalf("expected event type to be 'm.presence', got %s", x.Get("type").Str)
			}
		}
	}

	if len(users) != foundCounter {
		t.Fatalf("expected %d presence events, got %d: %+v", len(users), foundCounter, presenceEvents)
	}
}
