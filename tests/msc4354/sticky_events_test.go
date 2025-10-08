package tests

import (
	"fmt"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/tidwall/gjson"
)

var txnID int64 = 10000

func withStickyDuration(valMs int) func(qps url.Values) {
	return func(qps url.Values) {
		qps["org.matrix.msc4354.sticky_duration_ms"] = []string{strconv.Itoa(valMs)}
	}
}
func withDelayedEventDuration(valMs int) func(qps url.Values) {
	return func(qps url.Values) {
		qps["org.matrix.msc4140.delay"] = []string{strconv.Itoa(valMs)}
	}
}

func sendStickyEvent(t ct.TestLike, c *client.CSAPI, roomID string, e b.Event, opts ...func(qps url.Values)) string {
	t.Helper()
	txID := int(atomic.AddInt64(&txnID, 1))
	paths := []string{"_matrix", "client", "v3", "rooms", roomID, "send", e.Type, strconv.Itoa(txID)}
	if e.StateKey != nil {
		paths = []string{"_matrix", "client", "v3", "rooms", roomID, "state", e.Type, *e.StateKey}
	}
	qps := url.Values{}
	withStickyDuration(60000)(qps) // default 60s to make the event sticky.
	for _, o := range opts {
		o(qps)
	}
	res := c.MustDo(t, "PUT", paths, client.WithJSONBody(t, e.Content), client.WithQueries(qps))
	body := must.ParseJSON(t, res.Body)
	return body.Get("event_id").Str
}

func MustDoSlidingSync(t ct.TestLike, user *client.CSAPI, pos string) (gjson.Result, string) {
	t.Helper()
	body := map[string]interface{}{
		"lists": map[string]any{
			"any-key": map[string]any{
				"timeline_limit": 10,
				"required_state": [][]string{{"*", "*"}},
				"ranges":         [][]int{{0, 100}},
			},
		},
		"extensions": map[string]interface{}{
			"org.matrix.msc4354.sticky_events": map[string]any{
				"enabled": true,
			},
		},
	}
	qps := url.Values{"timeout": []string{"5000"}}
	if pos != "" {
		qps["pos"] = []string{pos}
	}
	httpResp := user.MustDo(
		t, "POST", []string{"_matrix", "client", "unstable", "org.matrix.simplified_msc3575", "sync"},
		client.WithJSONBody(t, body), client.WithQueries(qps),
	)
	respBody := must.ParseJSON(t, httpResp.Body)
	newPos := respBody.Get("pos").Str
	return respBody, newPos
}

// standardised response format for /sync and SSS
type syncResponse struct {
	stickyEvents   []gjson.Result
	timelineEvents []gjson.Result
}

func mustHaveStickyEventID(t ct.TestLike, eventID string, arr []gjson.Result) gjson.Result {
	t.Helper()
	for _, ev := range arr {
		if ev.Get("event_id").Str == eventID {
			// check it's sticky
			if !ev.Get("msc4354_sticky.duration_ms").Exists() {
				ct.Fatalf(t, "event '%s' exists but isn't sticky, missing 'sticky' key", eventID)
			}
			return ev
		}
	}
	ct.Fatalf(t, "event '%s' was not in array of length %d", eventID, len(arr))
	return gjson.Result{}
}

var stopMsg = b.Event{
	Type: "m.room.message",
	Content: map[string]interface{}{
		"msgtype": "m.text",
		"body":    "STOP",
	},
}

// Helper function to do /sync or SSS requests. Does a single /sync request.
// Returns the sticky/timeline events for the provided room ID, if any.
// Returns `true` if the timeline or sticky section included stopAtEventID.
func performSync(t ct.TestLike, cli *client.CSAPI, useSimplifiedSlidingSync bool, since, roomID, stopAtEventID string) (syncResp syncResponse, nextSince string, stop bool) {
	t.Helper()
	var timeline []gjson.Result
	var sticky []gjson.Result
	var resp gjson.Result
	if useSimplifiedSlidingSync {
		resp, nextSince = MustDoSlidingSync(t, cli, since)
		timeline = resp.Get("rooms." + client.GjsonEscape(roomID) + ".timeline").Array()
		sticky = resp.Get("extensions.org\\.matrix\\.msc4354\\.sticky_events.rooms." + client.GjsonEscape(roomID) + ".events").Array()
	} else {
		resp, nextSince = cli.MustSync(t, client.SyncReq{Since: since})
		timeline = resp.Get("rooms.join." + client.GjsonEscape(roomID) + ".timeline.events").Array()
		sticky = resp.Get("rooms.join." + client.GjsonEscape(roomID) + ".msc4354_sticky.events").Array()
		// t.Logf("%s\b", resp.Raw)
	}
	for _, ev := range append(append([]gjson.Result{}, timeline...), sticky...) {
		if ev.Get("event_id").Str == stopAtEventID {
			stop = true
			break
		}
	}
	return syncResponse{
		stickyEvents:   sticky,
		timelineEvents: timeline,
	}, nextSince, stop

}

// Helper function to sync until stopAtEventID is returned. Gathers all seen sticky events
// The intention is that tests can repeatedly hit this function until `true`,
// to gather up sticky events returned in the provided room.
func gatherSyncResults(t ct.TestLike, cli *client.CSAPI, useSimplifiedSlidingSync bool, roomID, stopAtEventID string) syncResponse {
	t.Helper()
	start := time.Now()
	timeout := 10 * time.Second
	var gatheredResponse syncResponse
	var since string
	var stop bool
	for {
		var resp syncResponse
		resp, since, stop = performSync(t, cli, useSimplifiedSlidingSync, since, roomID, stopAtEventID)
		gatheredResponse.stickyEvents = append(gatheredResponse.stickyEvents, resp.stickyEvents...)
		gatheredResponse.timelineEvents = append(gatheredResponse.timelineEvents, resp.timelineEvents...)
		if stop {
			return gatheredResponse
		}
		time.Sleep(100 * time.Millisecond)
		if time.Since(start) > timeout {
			ct.Fatalf(
				t, "gatherSyncResults: timed out waiting to see '%s', got %d timeline, %d sticky events",
				stopAtEventID, len(gatheredResponse.timelineEvents), len(gatheredResponse.stickyEvents),
			)
		}
	}
}

func forEachSync(t *testing.T, f func(t *testing.T, useSimplifiedSlidingSync bool)) {
	for _, useSimplifiedSlidingSync := range []bool{false, true} {
		subtestName := "normal sync"
		if useSimplifiedSlidingSync {
			subtestName = "simplified sliding sync"
		}
		t.Run(subtestName, func(t *testing.T) {
			f(t, useSimplifiedSlidingSync)
		})
	}
}

func TestStickyEvents(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	// Helper function to make a sticky state/message event
	makeStickyEvent := func(isStateEvent bool) b.Event {
		if isStateEvent {
			return b.Event{
				Type:     "m.room.sticky_state",
				StateKey: b.Ptr(""),
				Content: map[string]interface{}{
					"state": "This is a sticky state event",
				},
			}
		}
		return b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "This is a sticky event",
			},
		}
	}

	testCaseConfigurations := []struct {
		stickyEventIsStateEvent  bool
		useSimplifiedSlidingSync bool
	}{
		{stickyEventIsStateEvent: false, useSimplifiedSlidingSync: false},
		{stickyEventIsStateEvent: false, useSimplifiedSlidingSync: true},
		{stickyEventIsStateEvent: true, useSimplifiedSlidingSync: false},
		{stickyEventIsStateEvent: true, useSimplifiedSlidingSync: true},
	}
	for _, tc := range testCaseConfigurations {
		eventTypeMsg := "sticky message event"
		if tc.stickyEventIsStateEvent {
			eventTypeMsg = "sticky state event"
		}
		syncMsg := "with normal sync"
		if tc.useSimplifiedSlidingSync {
			syncMsg = "with simplified sliding sync"
		}
		t.Run(eventTypeMsg+" appears in timeline if no gaps "+syncMsg, func(t *testing.T) {
			roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
			stickyEvent := makeStickyEvent(tc.stickyEventIsStateEvent)
			stickyEventID := sendStickyEvent(t, alice, roomID, stickyEvent)
			stopEventID := alice.Unsafe_SendEventUnsynced(t, roomID, stopMsg)
			syncResp := gatherSyncResults(t, alice, tc.useSimplifiedSlidingSync, roomID, stopEventID)
			mustHaveStickyEventID(t, stickyEventID, syncResp.timelineEvents)
		})
		t.Run(eventTypeMsg+" appears in sticky if gaps "+syncMsg, func(t *testing.T) {
			roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
			stickyEvent := makeStickyEvent(tc.stickyEventIsStateEvent)
			stickyEventID := sendStickyEvent(t, alice, roomID, stickyEvent)
			for i := 0; i < 25; i++ {
				alice.Unsafe_SendEventUnsynced(t, roomID, b.Event{
					Type: "m.room.message",
					Content: map[string]interface{}{
						"msgtype": "m.text",
						"body":    fmt.Sprintf("msg %d", i),
					},
				})
			}
			stopEventID := alice.Unsafe_SendEventUnsynced(t, roomID, stopMsg)
			syncResp := gatherSyncResults(t, alice, tc.useSimplifiedSlidingSync, roomID, stopEventID)
			mustHaveStickyEventID(t, stickyEventID, syncResp.stickyEvents)
		})
		// now send unrelated normal events so the sticky event
	}
}

// Test MSC4354 works with MSC4140: Delayed Events
func TestDelayedStickyEvents(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	msg := "This is a delayed sticky event"
	stickyEvent := b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    msg,
		},
	}
	hasStickyEvent := func(arr []gjson.Result) bool {
		for _, stickyEvent := range arr {
			// we don't know the sticky event ID if it's delayed, so check for equality via the content.
			if stickyEvent.Get("content.body").Str == msg {
				return true
			}
		}
		return false
	}

	// it should have been delayed, so we shouldn't see the sticky event initially
	sendStickyEvent(t, alice, roomID, stickyEvent, withDelayedEventDuration(3000))
	stopEventID := alice.Unsafe_SendEventUnsynced(t, roomID, stopMsg)
	syncResp := gatherSyncResults(t, alice, false, roomID, stopEventID)
	if hasStickyEvent(syncResp.timelineEvents) {
		ct.Fatalf(t, "timeline had the sticky event, is delayed events supported?")
	}
	must.Equal(t, len(syncResp.stickyEvents), 0, "events were in sticky events when they shouldn't have been")

	// wait for the sticky event to send
	time.Sleep(4 * time.Second)

	for i := 0; i < 25; i++ {
		stopEventID = alice.Unsafe_SendEventUnsynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    fmt.Sprintf("msg %d", i),
			},
		})
	}

	// now it should appear in the sticky section. We don't know the sticky event ID,
	// so just look for any sticky event.
	syncResp = gatherSyncResults(t, alice, false, roomID, stopEventID)
	if !hasStickyEvent(syncResp.stickyEvents) {
		ct.Fatalf(t, "sticky events missing from /sync, did it send?")
	}
}

func TestUnsignedTTL(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	forEachSync(t, func(t *testing.T, useSimplifiedSlidingSync bool) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
		duration := 30000
		stickyEventID := sendStickyEvent(t, alice, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "This is a sticky event",
			},
		}, withStickyDuration(duration))
		syncResp := gatherSyncResults(t, alice, useSimplifiedSlidingSync, roomID, stickyEventID)
		stickyEvent := mustHaveStickyEventID(t, stickyEventID, syncResp.timelineEvents)
		must.MatchGJSON(t, stickyEvent,
			match.JSONKeyPresent("unsigned.msc4354_sticky_duration_ttl_ms"),
			match.JSONKeyTypeEqual("unsigned.msc4354_sticky_duration_ttl_ms", gjson.Number),
		)
		ttl := stickyEvent.Get("unsigned.msc4354_sticky_duration_ttl_ms").Int()
		if ttl < 0 || ttl > int64(duration) {
			ct.Fatalf(t, "unsigned.msc4354_sticky_duration_ttl_ms should be between 0-%d, got %d", duration, ttl)
		}
	})
}

// Test that newly joined users to history_visibility: joined rooms correctly see sticky events
// in the `sticky` section.
func TestStickyEventsIgnoreHistoryVisibility(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	forEachSync(t, func(t *testing.T, useSimplifiedSlidingSync bool) {
		// configure the room with joined history visibility, meaning you don't see events prior to your join.
		roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
		alice.SendEventSynced(t, roomID, b.Event{
			Type:     spec.MRoomHistoryVisibility,
			StateKey: b.Ptr(""),
			Content: map[string]interface{}{
				"history_visibility": "joined",
			},
		})
		// Make a timeline like
		// [ STICKY, MSG1, MSG2, ... MSG25, STICKY ]
		// and ensure newly joined users see both sticky events
		duration := 30000
		stickyEventIDNotInTimeline := sendStickyEvent(t, alice, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "This is a sticky event which is beyond the timeline limit",
			},
		}, withStickyDuration(duration))
		var lastEventIDBeforeBobJoins string
		for i := 0; i < 25; i++ {
			lastEventIDBeforeBobJoins = alice.Unsafe_SendEventUnsynced(t, roomID, b.Event{
				Type: "m.room.message",
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    fmt.Sprintf("msg %d", i),
				},
			})
		}
		stickyEventIDInTimeline := sendStickyEvent(t, alice, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "This is a sticky event which is inside the timeline limit",
			},
		}, withStickyDuration(duration))

		bob.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})

		stopEventID := alice.Unsafe_SendEventUnsynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "STOP",
			},
		})

		syncResp := gatherSyncResults(t, bob, useSimplifiedSlidingSync, roomID, stopEventID)
		mustHaveStickyEventID(t, stickyEventIDNotInTimeline, syncResp.stickyEvents)
		mustHaveStickyEventID(t, stickyEventIDInTimeline, syncResp.stickyEvents)
		// check the server actually implements history visibility correctly
		for _, ev := range syncResp.timelineEvents {
			if ev.Get("event_id").Str == lastEventIDBeforeBobJoins {
				ct.Fatalf(t, "bob saw normal event %d from before he joined, is history visibility working?", lastEventIDBeforeBobJoins)
			}
		}
	})
}

func TestStickyEventsSentToNewlyJoinedServers(t *testing.T) {
	deployment := complement.Deploy(t, 3)
	defer deployment.Destroy(t)

	// newJoiner will join via alice (hs1).
	// we include bob as a bystander server. hs2 will not process the /send_join response
	// but should receive the join event and realise it needs to send its own sticky events
	// to hs3.
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})
	newJoiner := deployment.Register(t, "hs3", helpers.RegistrationOpts{})

	forEachSync(t, func(t *testing.T, useSimplifiedSlidingSync bool) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
		bob.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})
		// Make a timeline like
		// [ STICKY, MSG1, MSG2, ... MSG25, STICKY ]
		// and ensure newly joined servers see both sticky events
		duration := 30000
		aliceStickyEventIDNotInTimeline := sendStickyEvent(t, alice, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "ALICE This is a sticky event which is beyond the timeline limit",
			},
		}, withStickyDuration(duration))
		bob.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, aliceStickyEventIDNotInTimeline))
		bobStickyEventIDNotInTimeline := sendStickyEvent(t, bob, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "BOB This is a sticky event which is beyond the timeline limit",
			},
		}, withStickyDuration(duration))
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, bobStickyEventIDNotInTimeline))
		for i := 0; i < 25; i++ {
			alice.SendEventSynced(t, roomID, b.Event{
				Type: "m.room.message",
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    fmt.Sprintf("msg %d", i),
				},
			})
		}
		aliceStickyEventIDInTimeline := sendStickyEvent(t, alice, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "ALICE This is a sticky event which is inside the timeline limit",
			},
		}, withStickyDuration(duration))
		bob.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, aliceStickyEventIDInTimeline))
		bobStickyEventIDInTimeline := sendStickyEvent(t, bob, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "BOB This is a sticky event which is inside the timeline limit",
			},
		}, withStickyDuration(duration))
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, bobStickyEventIDInTimeline))

		newJoiner.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})

		// wait until hs1 and hs2 see the join, as this will trigger the sending of sticky events
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(newJoiner.UserID, roomID))
		bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(newJoiner.UserID, roomID))
		t.Logf("alice's sticky events early=%s latest=%s", aliceStickyEventIDNotInTimeline, aliceStickyEventIDInTimeline)
		t.Logf("bob's sticky events early=%s latest=%s", bobStickyEventIDNotInTimeline, bobStickyEventIDInTimeline)

		// we need to wait for 2 things to happen:
		// - Alice to send her sticky events
		// - Bob to send his sticky events
		// But we don't want to use stop events for both, because we want to make sure that servers PROACTIVELY
		// send sticky events. In particular, perhaps Alice and Bob send their sticky events to NewJoiner but NewJoiner
		// puts them into a staging area and doesn't process them yet because they haven't processed the /send_join response
		// by the time they get the sticky events. We must make sure that NewJoiner processes this staging area without waiting
		// for another event. Sending a stop event will cause the queue for that server to be processed,
		// masking the problem. As a result, we will:
		// - send a stop event from alice and wait until we see the stop event.
		// - wait until we see bob's latest sticky event (no stop event)
		stopEventID := alice.Unsafe_SendEventUnsynced(t, roomID, stopMsg)
		syncResp := gatherSyncResults(t, newJoiner, useSimplifiedSlidingSync, roomID, stopEventID)
		allEvents := append(syncResp.stickyEvents, syncResp.timelineEvents...)
		// TODO: sometimes this fails because we seem to omit it from the sync response, but server logs suggest it is put in the timeline..?
		syncResp2 := gatherSyncResults(t, newJoiner, useSimplifiedSlidingSync, roomID, bobStickyEventIDInTimeline)
		allEvents = append(allEvents, syncResp2.timelineEvents...) // will have dupe events but this is fine.
		allEvents = append(allEvents, syncResp2.stickyEvents...)
		// we don't know which section they will appear in as it depends on many factors like:
		// - if the server automatically backfills from their join event, the latest sticky events will be in the timeline
		// - if other servers /send sticky events before the backfill, they will appear in 'sticky', else they will
		//   appear after the initial backfill so be in the timeline. This may or may not push out the latest sticky
		//   events depending on how far back they /get_missing_events.
		// as a result, we're just happy to see the sticky events, and don't care where they appear.
		mustHaveStickyEventID(t, aliceStickyEventIDInTimeline, allEvents)
		mustHaveStickyEventID(t, aliceStickyEventIDNotInTimeline, allEvents)
		mustHaveStickyEventID(t, bobStickyEventIDInTimeline, allEvents)
		mustHaveStickyEventID(t, bobStickyEventIDNotInTimeline, allEvents)
	})
}

func TestSoftFailedStickyEvents(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})
	sentinel := deployment.Register(t, "hs2", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})
	sentinel.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})

	// We want to concurrently:
	// - Alice kicks Bob
	// - Bob sends a sticky event.
	// To do this, we will pause each server so they can't communicate their events with each other.
	deployment.PauseServer(t, "hs2")
	alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "kick"}, client.WithJSONBody(t, map[string]string{
		"user_id": bob.UserID,
		"reason":  "Testing",
	}))
	deployment.PauseServer(t, "hs1")
	deployment.UnpauseServer(t, "hs2")
	stickyEventID := sendStickyEvent(t, bob, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "This is a sticky message sent whilst HS1 is offline",
		},
	})
	deployment.UnpauseServer(t, "hs1")

	// we want to check that the sticky event was in fact soft-failed. This is hard to do since it won't
	// come down /sync. Instead, we send a sentinel message from a different user and assert that we see
	// the sentinel event but not the sticky event.
	sentinelEventID := sentinel.Unsafe_SendEventUnsynced(t, roomID, stopMsg)
	syncResp := gatherSyncResults(t, alice, false, roomID, sentinelEventID)
	for _, ev := range append(syncResp.timelineEvents, syncResp.stickyEvents...) {
		if ev.Get("event_id").Str == stickyEventID {
			ct.Fatalf(t, "sticky event %s was not soft failed!", stickyEventID)
		}
	}

	// now we rejoin bob.
	// This should cause soft-failure of sticky events to be re-evaluated, causing it to appear in the 'sticky' section.
	bob.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
	forEachSync(t, func(t *testing.T, useSimplifiedSlidingSync bool) {
		syncResp := gatherSyncResults(t, alice, useSimplifiedSlidingSync, roomID, stickyEventID)
		mustHaveStickyEventID(t, stickyEventID, syncResp.stickyEvents)
	})
}

func TestStickyEventsChunkedInSync(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})
	_, bobSince := bob.MustSync(t, client.SyncReq{})
	t.Logf("before any sticky events: since=%s", bobSince)

	// This test assumes 3x /sync requests is enough to see all numMsgsToSend.
	// This test assumes 1x /sync will not return more than expectedMaxChunk sticky events.
	// As such, this test allows servers to return ceiling(numMsgsToSend/3) ~ expectedMaxChunk events
	// per /sync request.
	// Currently this means 84-230 per /sync.
	numMsgsToSend := 250
	expectedMaxChunk := 230

	// send many sticky events
	stickyEventIDs := make(map[string]bool)
	for i := 0; i < numMsgsToSend; i++ {
		eventID := sendStickyEvent(t, alice, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    fmt.Sprintf("msg %d", i),
			},
		}, withStickyDuration(1000*60*30))
		stickyEventIDs[eventID] = true
	}

	// do a single /sync request on bob
	resp, bobSince := bob.MustSync(t, client.SyncReq{Since: bobSince})
	t.Logf("after 1st /sync: since=%s", bobSince)

	removeStickyEvents := func(resp gjson.Result) {
		// bob should not see all the sticky events.
		// This includes timeline events (e.g N-25 sticky events + 25 timeline events is still N sticky events).
		sticky := resp.Get("rooms.join." + client.GjsonEscape(roomID) + ".msc4354_sticky.events").Array()
		for _, ev := range sticky {
			delete(stickyEventIDs, ev.Get("event_id").Str)
		}
		timeline := resp.Get("rooms.join." + client.GjsonEscape(roomID) + ".timeline.events").Array()
		for _, ev := range timeline {
			delete(stickyEventIDs, ev.Get("event_id").Str)
		}
		t.Logf("/sync contained %d sticky events and %d timeline events", len(sticky), len(timeline))
	}
	removeStickyEvents(resp)

	// we expect a max chunk of expectedMaxChunk
	if len(stickyEventIDs) < (numMsgsToSend - expectedMaxChunk) {
		ct.Fatalf(t, "sent %d sticky events, first sync contained %d, too many sticky events in one /sync", numMsgsToSend, numMsgsToSend-len(stickyEventIDs))
	}

	resp, bobSince = bob.MustSync(t, client.SyncReq{Since: bobSince, TimeoutMillis: "0"})
	t.Logf("after 2nd /sync: since=%s", bobSince)
	removeStickyEvents(resp)
	resp, _ = bob.MustSync(t, client.SyncReq{Since: bobSince, TimeoutMillis: "0"})
	t.Logf("after 3rd /sync: since=%s", bobSince)
	removeStickyEvents(resp)
	if len(stickyEventIDs) != 0 {
		ct.Fatalf(t, "failed to see all sticky events, missing %d", len(stickyEventIDs))
	}
}
