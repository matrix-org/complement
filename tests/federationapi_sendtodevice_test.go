package tests

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
)

func TestSendToDevice(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs2", "@bob:hs2")
	charlie := deployment.RegisterUser(t, "hs2", "charlie", "$uperSecretPass", false)

	// checks used to verify the sendToDevice messages are correct
	checks := []match.JSON{
		match.JSONKeyEqual("sender", alice.UserID),
		match.JSONKeyEqual("type", "m.room_key_request"),
		match.JSONKeyEqual("content.action", "request"),
		match.JSONKeyEqual("content.requesting_device_id", alice.DeviceID),
		match.JSONKeyEqual("content.request_id", "1"),
	}

	// sytest: Can recv a device message using /sync
	t.Run("Can recv a device message using /sync", func(t *testing.T) {
		// Create a m.room_key_request sendToDevice message
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"messages": map[string]interface{}{
				bob.UserID: map[string]interface{}{
					bob.DeviceID: map[string]interface{}{
						"action":               "request",
						"requesting_device_id": alice.DeviceID,
						"request_id":           "1",
					},
				},
			},
		})
		txnID := "1"
		// sytest: Can send a message directly to a device using PUT /sendToDevice
		alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "sendToDevice", "m.room_key_request", txnID}, reqBody)

		// verify the results from /sync
		nextBatch := bob.MustSyncUntil(t, client.SyncReq{}, verifyToDeviceResp(t, bob, checks, 1))
		// clear previously received sendToDevice events
		bob.MustSyncUntil(t, client.SyncReq{Since: nextBatch}, verifyToDeviceResp(t, bob, checks, 0))
	})

	// sytest: Can send a to-device message to two users which both receive it using /sync
	t.Run("Can send a to-device message to two users which both receive it using /sync", func(t *testing.T) {
		// Create a m.room_key_request sendToDevice message
		// Bob will get a message directly to the device, while Charlie will get the message on all devices
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"messages": map[string]interface{}{
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
		txnID := "2"
		alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "sendToDevice", "m.room_key_request", txnID}, reqBody)

		// verify the results from /sync
		nextBatch := bob.MustSyncUntil(t, client.SyncReq{}, verifyToDeviceResp(t, bob, checks, 1))
		// clear previously received sendToDevice events
		bob.MustSyncUntil(t, client.SyncReq{Since: nextBatch}, verifyToDeviceResp(t, bob, checks, 0))
		// Charlie syncs for the first time, so no need to clear events
		charlie.MustSyncUntil(t, client.SyncReq{}, verifyToDeviceResp(t, charlie, checks, 1))
	})

	t.Run("sendToDevice messages are not sent multiple times", func(t *testing.T) {
		// Create a m.room_key_request sendToDevice message
		reqBody := client.WithJSONBody(t, map[string]interface{}{
			"messages": map[string]interface{}{
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

		txnID := "3"
		alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "sendToDevice", "m.room_key_request", txnID}, reqBody)

		// Do an intialSync, we should receive one event
		bob.MustSyncUntil(t, client.SyncReq{}, verifyToDeviceResp(t, bob, checks, 1))

		// sync again, we should get the same event again, as we didn't advance "since"
		nextBatch := bob.MustSyncUntil(t, client.SyncReq{}, verifyToDeviceResp(t, bob, checks, 1))

		// advance the next_batch, we shouldn't get an event
		nextBatch = bob.MustSyncUntil(t, client.SyncReq{Since: nextBatch}, verifyToDeviceResp(t, bob, checks, 0))

		// send another toDevice event
		txnID = "4"
		alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "sendToDevice", "m.room_key_request", txnID}, reqBody)

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
		for i := 0; i < 10; i++ {
			// Create a m.room_key_request sendToDevice message
			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"messages": map[string]interface{}{
					bob.UserID: map[string]interface{}{
						bob.DeviceID: map[string]interface{}{
							"action":               "request",
							"requesting_device_id": alice.DeviceID,
							"request_id":           i,
						},
					},
				},
			})

			txnID := strconv.Itoa(10 + i)
			alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "sendToDevice", "m.room_key_request", txnID}, reqBody)
		}
		time.Sleep(time.Millisecond * 100) // Wait a bit for the server to process all messages

		verifyOrdering := func() match.JSON {
			var i int64
			return func(body []byte) error {
				if gotID := gjson.GetBytes(body, "content.request_id").Int(); gotID != i {
					return fmt.Errorf("expected request_id to be %d, got %d", i, gotID)
				}
				i++
				return nil
			}
		}()

		bob.MustSyncUntil(t, client.SyncReq{}, verifyToDeviceResp(t, bob, []match.JSON{verifyOrdering}, 10))
	})

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
