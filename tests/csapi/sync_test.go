package csapi_tests

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/federation"
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
	runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/1324
	// sytest: Can sync
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	filterID := createFilter(t, alice, map[string]interface{}{
		"room": map[string]interface{}{
			"timeline": map[string]interface{}{
				"limit": 10,
			},
		},
	})

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
			runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/1324
			t.Parallel()

			filterBob := createFilter(t, bob, map[string]interface{}{
				"room": map[string]interface{}{
					"timeline": map[string]interface{}{
						"limit": 10,
						"types": []string{"m.room.message"},
					},
					"state": map[string]interface{}{
						"types": []string{},
					},
				},
			})

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
			runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/1324
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
			runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/1324
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

		t.Run("sync should succeed even if the sync token points to a redaction of an unknown event", func(t *testing.T) {
			// this is a regression test for https://github.com/matrix-org/synapse/issues/12864
			//
			// The idea here is that we need a sync token which points to a redaction
			// for an event which doesn't exist. Such a redaction may not be served to
			// the client. This can lead to server bugs when the server tries to fetch
			// the event corresponding to the sync token.
			//
			// The C-S API does not permit us to generate such a redaction event, so
			// we have to poke it in from a federated server.
			//
			// The situation is complicated further by the very fact that we
			// cannot see the faulty redaction, and therefore cannot tell whether
			// our sync token includes it or not. The normal trick here would be
			// to send another (regular) event as a sentinel, and then if that sentinel
			// is returned by /sync, we can be sure the faulty event has also been
			// processed. However, that doesn't work here, because doing so will mean
			// that the sync token points to the sentinel rather than the redaction,
			// negating the whole point of the test.
			//
			// Instead, as a rough proxy, we send a sentinel in a *different* room.
			// There is no guarantee that the target server will process the events
			// in the order we send them, but in practice it seems to get close
			// enough.

			t.Parallel()

			// alice creates two rooms, which charlie (on our test server) joins
			srv := federation.NewServer(t, deployment,
				federation.HandleKeyRequests(),
				federation.HandleTransactionRequests(nil, nil),
			)
			cancel := srv.Listen()
			defer cancel()

			charlie := srv.UserID("charlie")

			redactionRoomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})
			redactionRoom := srv.MustJoinRoom(t, deployment, "hs1", redactionRoomID, charlie)

			sentinelRoomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})
			sentinelRoom := srv.MustJoinRoom(t, deployment, "hs1", sentinelRoomID, charlie)

			// charlie creates a bogus redaction, which he sends out, followed by
			// a good event - in another room - to act as a sentinel. It's not
			// guaranteed, but hopefully if the sentinel is received, so was the
			// redaction.
			redactionEvent := srv.MustCreateEvent(t, redactionRoom, b.Event{
				Type:    "m.room.redaction",
				Sender:  charlie,
				Content: map[string]interface{}{},
				Redacts: "$12345",
			})
			redactionRoom.AddEvent(redactionEvent)
			t.Logf("Created redaction event %s", redactionEvent.EventID())
			srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{redactionEvent.JSON()}, nil)

			sentinelEvent := srv.MustCreateEvent(t, sentinelRoom, b.Event{
				Type:    "m.room.test",
				Sender:  charlie,
				Content: map[string]interface{}{"body": "1234"},
			})
			sentinelRoom.AddEvent(sentinelEvent)
			t.Logf("Created sentinel event %s", sentinelEvent.EventID())
			srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{redactionEvent.JSON(), sentinelEvent.JSON()}, nil)

			// wait for the sentinel to arrive
			nextBatch := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(sentinelRoomID, sentinelEvent.EventID()))

			// charlie sends another batch of events to force a gappy sync.
			// We have to send 11 events to force a gap, since we use a filter with a timeline limit of 10 events.
			pdus := make([]json.RawMessage, 11)
			var lastSentEventId string
			for i := range pdus {
				ev := srv.MustCreateEvent(t, redactionRoom, b.Event{
					Type:    "m.room.message",
					Sender:  charlie,
					Content: map[string]interface{}{},
				})
				redactionRoom.AddEvent(ev)
				pdus[i] = ev.JSON()
				lastSentEventId = ev.EventID()
			}
			srv.MustSendTransaction(t, deployment, "hs1", pdus, nil)
			t.Logf("Sent filler events, with final event %s", lastSentEventId)

			// sync, starting from the same ?since each time, until the final message turns up.
			// This is basically an inlining of MustSyncUntil, with the key difference that we
			// keep the same ?since each time, instead of incrementally syncing on each pass.
			numResponsesReturned := 0
			start := time.Now()
			t.Logf("Will sync with since=%s", nextBatch)

			// This part of the test is flaky for workerised Synapse with the default 5 second timeout,
			// so bump it up to 10 seconds.
			alice.SyncUntilTimeout = 10 * time.Second

			for {
				if time.Since(start) > alice.SyncUntilTimeout {
					t.Fatalf("%s: timed out after %v. Seen %d /sync responses", alice.UserID, time.Since(start), numResponsesReturned)
				}
				// sync, using a filter with a limit smaller than the number of PDUs we sent.
				syncResponse, _ := alice.MustSync(t, client.SyncReq{Filter: filterID, Since: nextBatch})
				numResponsesReturned += 1
				timeline := syncResponse.Get("rooms.join." + client.GjsonEscape(redactionRoomID) + ".timeline")
				timelineEvents := timeline.Get("events").Array()

				if len(timelineEvents) > 0 {
					lastEventIdInSync := timelineEvents[len(timelineEvents)-1].Get("event_id").String()
					t.Logf("Iteration %d: /sync returned %d events, with final event %s", numResponsesReturned, len(timelineEvents), lastEventIdInSync)

					if lastEventIdInSync == lastSentEventId {
						// check we actually got a gappy sync - else this test isn't testing the right thing
						if !timeline.Get("limited").Bool() {
							t.Fatalf("Not a gappy sync after redaction")
						}
						break
					}
				} else {
					t.Logf("Iteration %d: /sync returned %d events", numResponsesReturned, len(timelineEvents))
				}

			}

			// that's it - we successfully did a gappy sync.
		})
	})
}

// Test presence from people in 2 different rooms in incremental sync
func TestPresenceSyncDifferentRooms(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	deployment.RegisterUser(t, "hs1", "charlie", "charliepassword", false)
	charlie := deployment.Client(t, "hs1", "@charlie:hs1")

	// Alice creates two rooms: one with her and Bob, and a second with her and Charlie.
	bobRoomID := alice.CreateRoom(t, struct{}{})
	charlieRoomID := alice.CreateRoom(t, struct{}{})
	nextBatch := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, bobRoomID), client.SyncJoinedTo(alice.UserID, charlieRoomID))

	alice.InviteRoom(t, bobRoomID, bob.UserID)
	alice.InviteRoom(t, charlieRoomID, charlie.UserID)
	bob.JoinRoom(t, bobRoomID, nil)
	charlie.JoinRoom(t, charlieRoomID, nil)

	nextBatch = alice.MustSyncUntil(t,
		client.SyncReq{Since: nextBatch},
		client.SyncJoinedTo(bob.UserID, bobRoomID),
		client.SyncJoinedTo(charlie.UserID, charlieRoomID),
	)

	// Bob and Charlie mark themselves as online.
	reqBody := client.WithJSONBody(t, map[string]interface{}{
		"presence": "online",
	})
	bob.DoFunc(t, "PUT", []string{"_matrix", "client", "v3", "presence", "@bob:hs1", "status"}, reqBody)
	charlie.DoFunc(t, "PUT", []string{"_matrix", "client", "v3", "presence", "@charlie:hs1", "status"}, reqBody)

	// Alice should see that Bob and Charlie are online. She may see this happen
	// simultaneously in one /sync response, or separately in two /sync
	// responses.
	seenBobOnline, seenCharlieOnline := false, false

	alice.MustSyncUntil(t, client.SyncReq{Since: nextBatch}, func(clientUserID string, sync gjson.Result) error {
		presenceArray := sync.Get("presence").Get("events").Array()
		if len(presenceArray) == 0 {
			return fmt.Errorf("presence.events is empty")
		}
		for _, x := range presenceArray {
			if x.Get("content").Get("presence").Str != "online" {
				continue
			}
			if x.Get("sender").Str == bob.UserID {
				seenBobOnline = true
			}
			if x.Get("sender").Str == charlie.UserID {
				seenCharlieOnline = true
			}
			if seenBobOnline && seenCharlieOnline {
				return nil
			}
		}
		return fmt.Errorf("all users not present yet, bob %t charlie %t", seenBobOnline, seenCharlieOnline)
	})
}

func TestRoomSummary(t *testing.T) {
	runtime.SkipIf(t, runtime.Synapse) // Currently more of a Dendrite test, so skip on Synapse
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	_, aliceSince := alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})
	roomID := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"invite": []string{bob.UserID},
	})
	aliceSince = alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince},
		client.SyncJoinedTo(alice.UserID, roomID),
		func(clientUserID string, syncResp gjson.Result) error {
			summary := syncResp.Get("rooms.join." + client.GjsonEscape(roomID) + ".summary")
			invitedUsers := summary.Get(client.GjsonEscape("m.invited_member_count")).Int()
			joinedUsers := summary.Get(client.GjsonEscape("m.joined_member_count")).Int()
			// We expect there to be one joined and one invited user
			if invitedUsers != 1 || joinedUsers != 1 {
				return fmt.Errorf("expected one invited and one joined user, got %d and %d: %v", invitedUsers, joinedUsers, summary.Raw)
			}
			return nil
		},
	)

	joinedCheck := func(clientUserID string, syncResp gjson.Result) error {
		summary := syncResp.Get("rooms.join." + client.GjsonEscape(roomID) + ".summary")
		invitedUsers := summary.Get(client.GjsonEscape("m.invited_member_count")).Int()
		joinedUsers := summary.Get(client.GjsonEscape("m.joined_member_count")).Int()
		// We expect there to be two joined and no invited user
		if invitedUsers != 0 || joinedUsers != 2 {
			return fmt.Errorf("expected no invited and two joined user, got %d and %d: %v", invitedUsers, joinedUsers, summary.Raw)
		}
		return nil
	}

	sinceToken := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
	bob.JoinRoom(t, roomID, []string{})
	// Verify Bob sees the correct room summary
	bob.MustSyncUntil(t, client.SyncReq{Since: sinceToken}, client.SyncJoinedTo(bob.UserID, roomID), joinedCheck)
	// .. and Alice as well.
	alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncJoinedTo(bob.UserID, roomID), joinedCheck)
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
