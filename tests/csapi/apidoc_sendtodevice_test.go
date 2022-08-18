package csapi_tests

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestSendToDevice(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")
	charlie := deployment.RegisterUser(t, "hs1", "charlie", "$uperSecretPass", false)

	// checks used to verify the sendToDevice messages are correct
	checks := []match.JSON{
		match.JSONKeyEqual("sender", alice.UserID),
		match.JSONKeyEqual("type", "m.room_key_request"),
		match.JSONKeyEqual("content.action", "request"),
		match.JSONKeyEqual("content.requesting_device_id", alice.DeviceID),
		match.JSONKeyEqual("content.request_id", "1"),
	}

	mustSendToDevice := sendToDevice(t)

	// sytest: Can recv a device message using /sync
	t.Run("Can recv a device message using /sync", func(t *testing.T) {
		// sytest: Can send a message directly to a device using PUT /sendToDevice
		i := mustSendToDevice(alice, bob.UserID, bob.DeviceID)
		var wantCount int64 = 1

		i -= wantCount
		nextBatch := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncToDeviceHas(
			func(msg gjson.Result) bool {
				gotReqID := msg.Get("content.request_id").Int()
				if gotReqID == i {
					i++ // we have the next request ID, look for another
				} else {
					// if we see any other request ID, whine about it e.g out-of-order requests
					t.Errorf("unexpected to-device request_id: %d, want %d", gotReqID, i)
				}
				return i == wantCount // terminate when we have seen all requests in order
			},
		))

		// Sync again to verify there are no more events.
		// This also removes sendToDevice events sent in this test case.
		syncResp, _ := bob.MustSync(t, client.SyncReq{Since: nextBatch})
		if syncResp.Get("to_device.events").Exists() {
			t.Fatal("expected there to be no more to_device.events")
		}
	})

	// sytest: Can send a to-device message to two users which both receive it using /sync
	t.Run("Can send a to-device message to two users which both receive it using /sync", func(t *testing.T) {
		// Bob will get a message directly to the device, while Charlie will get the message on all devices
		reqBody := client.WithJSONBody(t, map[string]map[string]interface{}{
			"messages": {
				bob.UserID: map[string]interface{}{
					bob.DeviceID: map[string]interface{}{
						"action":               "request",
						"requesting_device_id": alice.DeviceID,
						"request_id":           "1",
					},
				},
				charlie.UserID: map[string]interface{}{
					"*": map[string]interface{}{
						"action":               "request",
						"requesting_device_id": alice.DeviceID,
						"request_id":           "1",
					},
				},
			},
		})

		sendResp := alice.SendToDevice(t, "m.room_key_request", reqBody)
		must.MatchResponse(t, sendResp, match.HTTPResponse{StatusCode: http.StatusOK})

		// verify the results from /sync
		nextBatch := bob.MustSyncUntil(t, client.SyncReq{}, verifyToDeviceResp(t, bob, checks, 1))
		// clear previously received sendToDevice events
		bob.MustSyncUntil(t, client.SyncReq{Since: nextBatch}, verifyToDeviceResp(t, bob, checks, 0))
		// Charlie syncs for the first time, so no need to clear events
		charlie.MustSyncUntil(t, client.SyncReq{}, verifyToDeviceResp(t, charlie, checks, 1))
	})

	t.Run("sendToDevice messages are not sent multiple times", func(t *testing.T) {
		// Create a m.room_key_request sendToDevice message
		reqBody := client.WithJSONBody(t, map[string]map[string]interface{}{
			"messages": {
				bob.UserID: map[string]interface{}{
					bob.DeviceID: map[string]interface{}{
						"action":               "request",
						"requesting_device_id": alice.DeviceID,
						"request_id":           "2",
					},
				},
			},
		})
		checks = append(checks[:len(checks)-1], match.JSONKeyEqual("content.request_id", "2"))

		sendResp := alice.SendToDevice(t, "m.room_key_request", reqBody)
		must.MatchResponse(t, sendResp, match.HTTPResponse{StatusCode: http.StatusOK})

		// Do an intialSync, we should receive one event
		bob.MustSyncUntil(t, client.SyncReq{}, verifyToDeviceResp(t, bob, checks, 1))

		// sync again, we should get the same event again, as we didn't advance "since"
		nextBatch := bob.MustSyncUntil(t, client.SyncReq{}, verifyToDeviceResp(t, bob, checks, 1))

		// advance the next_batch, we shouldn't get an event
		nextBatch = bob.MustSyncUntil(t, client.SyncReq{Since: nextBatch}, verifyToDeviceResp(t, bob, checks, 0))

		// send another toDevice event
		sendResp = alice.SendToDevice(t, "m.room_key_request", reqBody)
		must.MatchResponse(t, sendResp, match.HTTPResponse{StatusCode: http.StatusOK})

		// Advance the sync token, we should get one event
		t.Logf("Syncing with %s", nextBatch)
		prevNextBatch := nextBatch
		nextBatch = bob.MustSyncUntil(t, client.SyncReq{Since: nextBatch}, verifyToDeviceResp(t, bob, checks, 1))

		// act as if the sync "failed", so we're using the previous next batch
		t.Logf("Syncing with %s", prevNextBatch)
		bob.MustSyncUntil(t, client.SyncReq{Since: prevNextBatch}, verifyToDeviceResp(t, bob, checks, 1))

		// advance since, to clear previously received events
		t.Logf("Syncing with %s", nextBatch)
		bob.MustSyncUntil(t, client.SyncReq{Since: nextBatch}, verifyToDeviceResp(t, bob, checks, 0))

		// since we advanced "since", this should return no events anymore
		t.Logf("Syncing with %s", prevNextBatch)
		bob.MustSyncUntil(t, client.SyncReq{Since: prevNextBatch}, verifyToDeviceResp(t, bob, checks, 0))
	})

	t.Run("sendToDevice messages are ordered by arrival", func(t *testing.T) {
		var wantCount int64 = 10

		var i int64
		for j := 0; j < int(wantCount); j++ {
			i = mustSendToDevice(alice, bob.UserID, bob.DeviceID)
		}
		i -= wantCount

		nextBatch := bob.MustSyncUntil(t, client.SyncReq{}, client.SyncToDeviceHas(
			func(msg gjson.Result) bool {
				gotReqID := msg.Get("content.request_id").Int()
				if gotReqID == i {
					i++ // we have the next request ID, look for another
				} else {
					// if we see any other request ID, whine about it e.g out-of-order requests
					t.Errorf("unexpected to-device request_id: %d, want %d", gotReqID, i)
				}
				return i == wantCount // terminate when we have seen all requests in order
			},
		))

		// Sync again to verify there are no more events
		syncResp, _ := bob.MustSync(t, client.SyncReq{Since: nextBatch})
		if syncResp.Get("to_device.events").Exists() {
			t.Fatal("expected there to be no more to_device.events")
		}
	})

}

func sendToDevice(t *testing.T) func(sender *client.CSAPI, userID, deviceID string) int64 {
	var reqID int64 = 0
	return func(sender *client.CSAPI, userID, deviceID string) int64 {
		// Create a m.room_key_request sendToDevice message
		reqBody := client.WithJSONBody(t, map[string]map[string]interface{}{
			"messages": {
				userID: map[string]interface{}{
					deviceID: map[string]interface{}{
						"action":               "request",
						"requesting_device_id": sender.DeviceID,
						"request_id":           reqID,
					},
				},
			},
		})
		sendResp := sender.SendToDevice(t, "m.room_key_request", reqBody)
		must.MatchResponse(t, sendResp, match.HTTPResponse{StatusCode: http.StatusOK})
		reqID++
		return reqID
	}
}

func verifyToDeviceResp(t *testing.T, user *client.CSAPI, checks []match.JSON, wantMessageCount int64) client.SyncCheckOpt {
	return func(userID string, syncResp gjson.Result) error {
		t.Helper()
		toDevice := syncResp.Get("to_device.events")
		if !toDevice.Exists() {
			if wantMessageCount > 0 && len(toDevice.Array()) == 0 {
				return fmt.Errorf("no to_device.events found: %s", syncResp.Get("to_device").Raw)
			}
		}
		if count := len(toDevice.Array()); int64(count) != wantMessageCount {
			t.Fatalf("(%s - %s) expected %d to_device.events, got %d - %v", user.UserID, user.DeviceID, wantMessageCount, count, toDevice.Raw)
		}
		for _, message := range toDevice.Array() {
			for _, check := range checks {
				if err := check([]byte(message.Raw)); err != nil {
					return fmt.Errorf("%v: %s", err, message.Raw)
				}
			}
		}
		return nil
	}
}
