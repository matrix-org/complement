package csapi_tests

import (
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

	mustSendToDevice := sendToDevice(t)

	// sytest: Can recv a device message using /sync
	t.Run("Can recv a device message using /sync", func(t *testing.T) {
		// sytest: Can send a message directly to a device using PUT /sendToDevice
		i := mustSendToDevice(alice, bob.UserID, bob.DeviceID)
		countSendToDeviceMessages(t, bob, client.SyncReq{}, i, 1, true)
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
						"request_id":           2,
					},
				},
				charlie.UserID: map[string]interface{}{
					"*": map[string]interface{}{
						"action":               "request",
						"requesting_device_id": alice.DeviceID,
						"request_id":           2,
					},
				},
			},
		})

		sendResp := alice.SendToDevice(t, "m.room_key_request", reqBody)
		must.MatchResponse(t, sendResp, match.HTTPResponse{StatusCode: http.StatusOK})

		// Check that we received the expected request ID for both users
		countSendToDeviceMessages(t, bob, client.SyncReq{}, 2, 1, true)
		countSendToDeviceMessages(t, charlie, client.SyncReq{}, 2, 1, true)
	})

	t.Run("sendToDevice messages are not sent multiple times", func(t *testing.T) {
		wantReqID := mustSendToDevice(alice, bob.UserID, bob.DeviceID)
		//sendResp := alice.SendToDevice(t, "m.room_key_request", reqBody)
		//must.MatchResponse(t, sendResp, match.HTTPResponse{StatusCode: http.StatusOK})

		// Do an intialSync, we should receive one event
		countSendToDeviceMessages(t, bob, client.SyncReq{}, wantReqID, 1, false)

		// sync again, we should get the same event again, as we didn't advance "since"
		nextBatch := countSendToDeviceMessages(t, bob, client.SyncReq{}, wantReqID, 1, false)

		// advance the next_batch, we shouldn't get an event
		syncResp, nextBatch := bob.MustSync(t, client.SyncReq{Since: nextBatch})
		toDev := syncResp.Get("to_device.events")
		if toDev.Exists() {
			t.Fatalf("expected no toDevice events, got %d", len(toDev.Array()))
		}

		// send another toDevice event
		wantReqID = mustSendToDevice(alice, bob.UserID, bob.DeviceID)

		// Advance the sync token, we should get one event
		prevNextBatch := nextBatch
		countSendToDeviceMessages(t, bob, client.SyncReq{Since: nextBatch}, wantReqID, 1, false)

		// act as if the sync "failed", so we're using the previous next batch
		countSendToDeviceMessages(t, bob, client.SyncReq{Since: prevNextBatch}, wantReqID, 1, true)
	})

	t.Run("sendToDevice messages are ordered by arrival", func(t *testing.T) {
		var wantCount int64 = 10

		var i int64
		for j := 0; j < int(wantCount); j++ {
			i = mustSendToDevice(alice, bob.UserID, bob.DeviceID)
		}
		i = (i - wantCount) + 1

		countSendToDeviceMessages(t, bob, client.SyncReq{}, i, wantCount, true)
	})

}

func countSendToDeviceMessages(t *testing.T, bob *client.CSAPI, req client.SyncReq, i int64, wantCount int64, withClear bool) (nextBatch string) {
	t.Helper()
	nextBatch = bob.MustSyncUntil(t, req, client.SyncToDeviceHas(
		func(msg gjson.Result) bool {
			t.Helper()
			gotReqID := msg.Get("content.request_id").Int()
			if gotReqID == i {
				i++ // we have the next request ID, look for another
				if i > wantCount {
					return true
				}
			} else {
				// if we see any other request ID, whine about it e.g out-of-order requests
				t.Errorf("unexpected to-device request_id: %d, want %d", gotReqID, i)
			}
			return i == wantCount // terminate when we have seen all requests in order
		},
	))
	if !withClear {
		return nextBatch
	}

	// Sync again to verify there are no more events
	syncResp, _ := bob.MustSync(t, client.SyncReq{Since: nextBatch})
	if syncResp.Get("to_device.events").Exists() {
		t.Fatal("expected there to be no more to_device.events")
	}
	return nextBatch
}

func sendToDevice(t *testing.T) func(sender *client.CSAPI, userID, deviceID string) int64 {
	var reqID int64 = 0
	return func(sender *client.CSAPI, userID, deviceID string) int64 {
		reqID++
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
		return reqID
	}
}
