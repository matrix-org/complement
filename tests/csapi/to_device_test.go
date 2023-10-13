package csapi_tests

import (
	"reflect"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
)

// sytest: Can send a message directly to a device using PUT /sendToDevice
// sytest: Can recv a device message using /sync
// sytest: Can send a to-device message to two users which both receive it using /sync
func TestToDeviceMessages(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	charlie := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "charlie",
		Password:        "charliepassword",
	})

	_, bobSince := bob.MustSync(t, client.SyncReq{TimeoutMillis: "0"})
	_, charlieSince := charlie.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

	content := map[string]interface{}{
		"my_key": "my_value",
	}

	alice.MustSendToDeviceMessages(t, "my.test.type", map[string]map[string]map[string]interface{}{
		bob.UserID: {
			bob.DeviceID: content,
		},
		charlie.UserID: {
			charlie.DeviceID: content,
		},
	})

	checkEvent := func(result gjson.Result) bool {
		if result.Get("type").Str != "my.test.type" {
			return false
		}

		evContentRes := result.Get("content")

		if !evContentRes.Exists() || !evContentRes.IsObject() {
			return false
		}

		evContent := evContentRes.Value()

		return reflect.DeepEqual(evContent, content)
	}

	bob.MustSyncUntil(t, client.SyncReq{Since: bobSince}, client.SyncToDeviceHas(alice.UserID, checkEvent))

	charlie.MustSyncUntil(t, client.SyncReq{Since: charlieSince}, client.SyncToDeviceHas(alice.UserID, checkEvent))

}
