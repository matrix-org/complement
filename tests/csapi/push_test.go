package csapi_tests

import (
	"fmt"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

// sytest: Getting push rules doesn't corrupt the cache SYN-390
func TestPushRuleCacheHealth(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	// Set a global push rule
	alice.SetPushRule(t, "global", "sender", alice.UserID, map[string]interface{}{
		"actions": []string{"dont_notify"},
	}, "", "")

	// Fetch the rule once and check its contents
	must.MatchGJSON(t, alice.GetAllPushRules(t), match.JSONKeyEqual("global.sender.0.actions.0", "dont_notify"))

	// Fetch the rule and check its contents again. It should not have changed.
	must.MatchGJSON(t, alice.GetAllPushRules(t), match.JSONKeyEqual("global.sender.0.actions.0", "dont_notify"))
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

			alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "pushrules", "global", "room", "!foo:example.com"}, body)
		})
	})

	// sytest: Disabling a push rule wakes up an incremental /sync
	t.Run("Disabling a push rule wakes up an incremental /sync", func(t *testing.T) {
		nextBatch = checkWokenUp(t, alice, client.SyncReq{Since: nextBatch, TimeoutMillis: "10000"}, func() {
			body := client.WithJSONBody(t, map[string]interface{}{
				"enabled": false,
			})

			alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "pushrules", "global", "room", "!foo:example.com", "enabled"}, body)
		})
	})

	// sytest: Enabling a push rule wakes up an incremental /sync
	t.Run("Enabling a push rule wakes up an incremental /sync", func(t *testing.T) {
		nextBatch = checkWokenUp(t, alice, client.SyncReq{Since: nextBatch, TimeoutMillis: "10000"}, func() {
			body := client.WithJSONBody(t, map[string]interface{}{
				"enabled": true,
			})

			alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "pushrules", "global", "room", "!foo:example.com", "enabled"}, body)
		})
	})

	// sytest: Setting actions for a push rule wakes up an incremental /sync
	t.Run("Setting actions for a push rule wakes up an incremental /sync", func(t *testing.T) {
		checkWokenUp(t, alice, client.SyncReq{Since: nextBatch, TimeoutMillis: "10000"}, func() {
			body := client.WithJSONBody(t, map[string]interface{}{
				"actions": []string{"dont_notify"},
			})

			alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "pushrules", "global", "room", "!foo:example.com", "actions"}, body)
		})
	})
}

func checkWokenUp(t *testing.T, csapi *client.CSAPI, syncReq client.SyncReq, fn func()) (nextBatch string) {
	t.Helper()
	errChan := make(chan error, 1)
	syncStarted := make(chan struct{})
	go func() {
		defer close(errChan)
		defer close(syncStarted)

		var syncResp gjson.Result
		syncStarted <- struct{}{}
		syncResp, nextBatch = csapi.MustSync(t, syncReq)
		// get the first result where the type is m.push_rules
		pushrules := syncResp.Get(`account_data.events.#(type=="m.push_rules").content.global`)
		if !pushrules.Exists() {
			errChan <- fmt.Errorf("no pushrules found in sync response: %s", syncResp.Raw)
			return
		}
		errChan <- nil
	}()
	// Try to wait for the sync to actually start, so that we test wakeups
	select {
	case <-syncStarted:
		fn()
	case <-time.After(time.Second * 5):
		// even though this should mostly be impossible, make sure we have a timeout
		t.Fatalf("goroutine didn't start")
	}

	// Try to wait for the sync to return or timeout after 15 seconds,
	// as the above tests are using a timeout of 10 seconds
	select {
	case err := <-errChan:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second * 15):
		t.Errorf("sync failed to return")
	}

	return nextBatch
}
