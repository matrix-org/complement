package tests

import (
	"encoding/json"
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
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var txnID int64 = 10000

func withStickyDuration(valMs int) func(qps url.Values) {
	return func(qps url.Values) {
		qps["msc4354_stick_duration_ms"] = []string{strconv.Itoa(valMs)}
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

func mustHaveStickyEventID(t ct.TestLike, eventID string, arr []gjson.Result) {
	t.Helper()
	for _, ev := range arr {
		if ev.Get("event_id").Str == eventID {
			// check it's sticky
			if !ev.Get("msc4354_sticky.duration_ms").Exists() {
				ct.Fatalf(t, "event '%s' exists but isn't sticky, missing 'sticky' key", eventID)
			}
			return
		}
	}
	ct.Fatalf(t, "event '%s' was not in array of length %d", eventID, len(arr))
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
// Returns `true` if the timeline included stopAtEventID.
func performSync(t ct.TestLike, cli *client.CSAPI, useSimplifiedSlidingSync bool, since, roomID, stopAtEventID string) (syncResp syncResponse, nextSince string, stop bool) {
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
	for _, ev := range timeline {
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
	start := time.Now()
	timeout := 5 * time.Second
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

func TestSoftFailedStickyEvents(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(
			nil, nil,
		),
	)
	cancel := srv.Listen()
	defer cancel()

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := srv.UserID("bob")

	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	srvRoom := srv.MustJoinRoom(t, deployment, "hs1", roomID, bob)
	latestEventID := srvRoom.ForwardExtremities[0]
	t.Logf("latestEventID = %s", latestEventID)

	// Alice kicks Bob. Concurrently, Bob sends a sticky event. The sticky event is soft-failed.
	alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "kick"}, client.WithJSONBody(t, map[string]string{
		"user_id": bob,
		"reason":  "Testing",
	}))
	stickyPDU := srv.MustCreateEvent(t, srvRoom, federation.Event{
		Type:   "m.room.message",
		Sender: bob,
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Bob's sticky event",
		},
		PrevEvents: []string{latestEventID},
		AuthEvents: []string{
			srvRoom.CurrentState(spec.MRoomCreate, "").EventID(),
			srvRoom.CurrentState(spec.MRoomPowerLevels, "").EventID(),
			latestEventID, // bob's join
		},
	})
	// XXX: this doesn't work as it trips the content hash check
	stickyJSON := stickyPDU.JSON()
	stickyJSON, err := sjson.SetBytes(stickyJSON, "msc4354_sticky.duration_ms", 600000)
	must.NotError(t, "failed to set sticky field", err)
	srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{stickyJSON}, nil)
	t.Logf("sticky event ID: %s", stickyPDU.EventID())

	// now send 25 timeline events to shift the timeline.
	for i := 0; i < 25; i++ {
		alice.Unsafe_SendEventUnsynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    fmt.Sprintf("msg %d", i),
			},
		})
	}
	// now Bob rejoins. We should see the sticky event in the sticky section.
	srv.MustJoinRoom(t, deployment, "hs1", roomID, bob)

	stopEventID := alice.Unsafe_SendEventUnsynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "STOP",
		},
	})

	syncResp := gatherSyncResults(t, alice, false, roomID, stopEventID)
	mustHaveStickyEventID(t, stickyPDU.EventID(), syncResp.stickyEvents)

}
