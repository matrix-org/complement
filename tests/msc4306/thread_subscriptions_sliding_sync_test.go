package tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/tidwall/gjson"
)

func MustDoSlidingSync(t *testing.T, user *client.CSAPI, pos string, thread_subs_ext map[string]interface{}) (string, gjson.Result) {
	body := map[string]interface{}{
		"extensions": map[string]interface{}{
			"io.element.msc4308.thread_subscriptions": thread_subs_ext,
		},
	}
	if pos != "" {
		body["pos"] = pos
	}
	resp := user.MustDo(t, "POST", []string{"_matrix", "client", "unstable", "org.matrix.simplified_msc3575", "sync"}, client.WithJSONBody(t, body))
	respBody := client.ParseJSON(t, resp)

	newPos := client.GetJSONFieldStr(t, respBody, "pos")
	extension := client.GetOptionalJSONFieldObject(t, respBody, "extensions.io\\.element\\.msc4308\\.thread_subscriptions")
	if !extension.Exists() {
		// Missing extension is semantically the same as an empty one
		// So eliminate the nil for simplicity
		extension = gjson.Parse("{}")
	}
	return newPos, extension
}

// Tests the thread subscriptions extension to sliding sync, introduced in MSC4308
// but treated as the same unit as MSC4306.
func TestMSC4308ThreadSubscriptionsSlidingSync(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	t.Run("Receives thread subscriptions over initial sliding sync", func(t *testing.T) {
		alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
		roomID := alice.MustCreateRoom(t, map[string]interface{}{})
		threadRootID := alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "what do you think? reply in a thread!",
			},
		})

		// Subscribe to the thread manually
		alice.MustDo(t, "PUT", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"}, client.WithJSONBody(t, map[string]interface{}{}))

		_, thread_subscription_ext := MustDoSlidingSync(t, alice, "", map[string]interface{}{
			"enabled": true,
			"limit":   2,
		})

		must.MatchGJSON(t, ext,
			match.JSONKeyTypeEqual("subscribed."+gjson.Escape(roomID)+"."+gjson.Escape(threadRootID)+".bump_stamp", gjson.Number),
			match.JSONKeyEqual("subscribed."+gjson.Escape(roomID)+"."+gjson.Escape(threadRootID)+".automatic", false),
		)
	})

	t.Run("Receives thread subscriptions over incremental sliding sync", func(t *testing.T) {
		alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
		roomID := alice.MustCreateRoom(t, map[string]interface{}{})
		threadRootID := alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "what do you think? reply in a thread!",
			},
		})

		newPos, ext := MustDoSlidingSync(t, alice, "", map[string]interface{}{
			"enabled": true,
			"limit":   2,
		})
		must.MatchGJSON(t, ext,
			match.JSONKeyMissing("subscribed"))

		// Subscribe to the thread manually
		alice.MustDo(t, "PUT", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"}, client.WithJSONBody(t, map[string]interface{}{}))

		_, ext = MustDoSlidingSync(t, alice, newPos, map[string]interface{}{
			"enabled": true,
			"limit":   2,
		})

		must.MatchGJSON(t, ext,
			match.JSONKeyTypeEqual("subscribed."+gjson.Escape(roomID)+"."+gjson.Escape(threadRootID)+".bump_stamp", gjson.Number),
			match.JSONKeyEqual("subscribed."+gjson.Escape(roomID)+"."+gjson.Escape(threadRootID)+".automatic", false),
		)
	})
}
