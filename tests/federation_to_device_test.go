package tests

import (
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/tidwall/gjson"
)

// Test that to-device messages can go from one homeserver to another.
func TestToDeviceMessagesOverFederation(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	testCases := []struct {
		name            string
		makeUnreachable func(t *testing.T)
		makeReachable   func(t *testing.T)
	}{
		{
			name:            "good connectivity",
			makeUnreachable: func(t *testing.T) {},
			makeReachable:   func(t *testing.T) {},
		},
		{
			// cut networking but keep in-memory state
			name: "interrupted connectivity",
			makeUnreachable: func(t *testing.T) {
				deployment.StopServer(t, "hs2")
			},
			makeReachable: func(t *testing.T) {
				deployment.StartServer(t, "hs2")
			},
		},
		{
			// interesting because this nukes memory
			name: "stopped server",
			makeUnreachable: func(t *testing.T) {
				deployment.StopServer(t, "hs2")
			},
			makeReachable: func(t *testing.T) {
				// kick over the sending server first to see if the server
				// remembers to resend on startup
				deployment.StopServer(t, "hs1")
				deployment.StartServer(t, "hs1")
				// now make the receiving server reachable.
				deployment.StartServer(t, "hs2")
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
				LocalpartSuffix: "alice",
			})
			bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{
				LocalpartSuffix: "bob",
			})
			// it might take a while for retries, so keep on syncing!
			bob.SyncUntilTimeout = 30 * time.Second

			_, bobSince := bob.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

			content := map[string]interface{}{
				"my_key": "my_value",
			}

			tc.makeUnreachable(t)

			alice.MustSendToDeviceMessages(t, "my.test.type", map[string]map[string]map[string]interface{}{
				bob.UserID: {
					bob.DeviceID: content,
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
			// just in case the server returns 200 OK before flushing to disk, give it a grace period.
			// This is too nice of us given in the real world no grace is provided..
			time.Sleep(time.Second)

			tc.makeReachable(t)

			var completed atomic.Bool
			go func() {
				time.Sleep(10 * time.Second)
				if completed.Load() {
					return
				}
				// maybe kicking the server will make things work if we're still waiting after 10s
				alice.MustSendToDeviceMessages(t, "kick.type", map[string]map[string]map[string]interface{}{
					bob.UserID: {
						bob.DeviceID: content,
					},
				})
			}()

			bob.MustSyncUntil(t, client.SyncReq{Since: bobSince}, func(clientUserID string, topLevelSyncJSON gjson.Result) error {
				t.Logf("%s", topLevelSyncJSON.Raw)
				return client.SyncToDeviceHas(alice.UserID, checkEvent)(clientUserID, topLevelSyncJSON)
			})
			completed.Store(true)
		})
	}
}
