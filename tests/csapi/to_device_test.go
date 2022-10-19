package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/tidwall/gjson"
)

// sytest: Can send a message directly to a device using PUT /sendToDevice
func TestSendToDevice(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	alice.SendToDevice(t, "m.room_key", bob.UserID, bob.DeviceID, map[string]interface{}{
		"algorithm":   "m.megolm.v1.aes-sha2",
		"room_id":     "!Cuyf34gef24t:localhost",
		"session_id":  "dummy",
		"session_key": "dummy2",
	})

	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncToDeviceHas(func(result gjson.Result) bool {
		return result.Get("type").Str == "m.room_key" &&
			result.Get("sender").Str == "@alice:hs1" &&
			result.Get("content.algorithm").Str == "m.megolm.v1.aes-sha2"
	}))
}
