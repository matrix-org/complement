package csapi_tests

import (
	"fmt"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/matrix-org/complement/runtime"
)

// @shadowjonathan: do we need this test anymore?
// sytest: Getting push rules doesn't corrupt the cache SYN-390
func TestPushRuleCacheHealth(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // Dendrite does not support push notifications (yet)

	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "pushrules", "global", "sender", alice.UserID}, client.WithJSONBody(t, map[string]interface{}{
		"actions": []string{"dont_notify"},
	}))

	// the extra "" is to make sure the submitted URL ends with a trailing slash
	res := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "pushrules", ""})

	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyEqual("global.sender.0.actions.0", "dont_notify"),
		},
	})

	res = alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "pushrules", ""})

	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyEqual("global.sender.0.actions.0", "dont_notify"),
		},
	})
}

func TestPushSync(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	var syncResp gjson.Result
	var nextBatch string

	// sytest: Push rules come down in an initial /sync
	t.Run("Push rules come down in an initial /sync", func(t *testing.T) {
		syncResp, nextBatch = alice.MustSync(t, client.SyncReq{})
		// get the first result where the type is m.push_rules
		pushrules := syncResp.Get(`account_data.events.#(type=="m.push_rules").content.global`)
		if !pushrules.Exists() {
			t.Fatalf("no pushrules found in sync response: %s", syncResp.Raw)
		}
	})

	// sytest: Adding a push rule wakes up an incremental /sync
	t.Run("Adding a push rule wakes up an incremental /sync", func(t *testing.T) {
		nextBatch = checkWokenUp(t, alice, client.SyncReq{Since: nextBatch, TimeoutMillis: "10000"}, func() {
			body := client.WithJSONBody(t, map[string]interface{}{
				"actions": []string{"notify"},
			})

			alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "pushrules", "global", "room", "!foo:example.com"}, body)
		})
	})

	// sytest: Disabling a push rule wakes up an incremental /sync
	t.Run("Disabling a push rule wakes up an incremental /sync", func(t *testing.T) {
		nextBatch = checkWokenUp(t, alice, client.SyncReq{Since: nextBatch, TimeoutMillis: "10000"}, func() {
			body := client.WithJSONBody(t, map[string]interface{}{
				"enabled": false,
			})

			alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "pushrules", "global", "room", "!foo:example.com", "enabled"}, body)
		})
	})

	// sytest: Enabling a push rule wakes up an incremental /sync
	t.Run("Enabling a push rule wakes up an incremental /sync", func(t *testing.T) {
		nextBatch = checkWokenUp(t, alice, client.SyncReq{Since: nextBatch, TimeoutMillis: "10000"}, func() {
			body := client.WithJSONBody(t, map[string]interface{}{
				"enabled": true,
			})

			alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "pushrules", "global", "room", "!foo:example.com", "enabled"}, body)
		})
	})

	// sytest: Setting actions for a push rule wakes up an incremental /sync
	t.Run("Setting actions for a push rule wakes up an incremental /sync", func(t *testing.T) {
		checkWokenUp(t, alice, client.SyncReq{Since: nextBatch, TimeoutMillis: "10000"}, func() {
			body := client.WithJSONBody(t, map[string]interface{}{
				"actions": []string{"dont_notify"},
			})

			alice.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "pushrules", "global", "room", "!foo:example.com", "actions"}, body)
		})
	})
}

func checkWokenUp(t *testing.T, csapi *client.CSAPI, syncReq client.SyncReq, fn func()) (nextBatch string) {
	t.Helper()
	errChan := make(chan error, 1)
	go func() {
		var syncResp gjson.Result
		syncResp, nextBatch = csapi.MustSync(t, syncReq)
		// get the first result where the type is m.push_rules
		pushrules := syncResp.Get(`account_data.events.#(type=="m.push_rules").content.global`)
		if !pushrules.Exists() {
			errChan <- fmt.Errorf("no pushrules found in sync response: %s", syncResp.Raw)
			return
		}
		errChan <- nil
	}()

	fn()

	if err := <-errChan; err != nil {
		t.Fatal(err)
	}
	return nextBatch
}
