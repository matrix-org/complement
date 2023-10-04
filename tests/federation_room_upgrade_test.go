package tests

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

// NOTE: this test falls under a "should" guarantee from the spec: https://spec.matrix.org/v1.4/client-server-api/#server-behaviour-16
// > When a user joins the new room, the server SHOULD automatically transfer/replicate some of the user’s personalized settings such as notifications, tags, etc.
//
// sytest: $user_type user has push rules copied to upgraded room
func TestRoomUpgradeCopiesPushRules(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationTwoLocalOneRemote)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")
	charlie := deployment.Client(t, "hs2", "@charlie:hs2")

	doTestWith := func(t *testing.T, user *client.CSAPI) {
		roomID := alice.CreateRoom(t, map[string]interface{}{
			"preset": "public_chat",
		})

		user.JoinRoom(t, roomID, []string{"hs1"})
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(user.UserID, roomID))

		user.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "pushrules", "global", "room", roomID},
			client.WithJSONBody(t, map[string]interface{}{
				"actions": []string{"notify"},
			}),
		)

		_, sinceToken := alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

		resp := alice.MustDoFunc(
			t,
			"POST",
			[]string{"_matrix", "client", "v3", "rooms", roomID, "upgrade"},
			client.WithJSONBody(t, map[string]string{
				"new_version": "6",
			}),
		)

		upgradeJson := gjson.ParseBytes(must.ParseJSON(t, resp.Body))

		must.MatchGJSON(
			t,
			upgradeJson,
			match.JSONKeyPresent("replacement_room"),
			match.JSONKeyTypeEqual("replacement_room", gjson.String),
		)

		replacementRoom := upgradeJson.Get("replacement_room").Str

		stateExists := func(evType string) func(gjson.Result) bool {
			return func(ev gjson.Result) bool {
				return ev.Get("type").Str == evType && ev.Get("state_key").Exists()
			}
		}

		// Note: we cannot check for the following state here, as it's only prescribed as an "implementation detail" by the spec:
		// - m.room.guest_access
		// - m.room.history_visibility
		// - m.room.join_rules
		alice.MustSyncUntil(t, client.SyncReq{Since: sinceToken}, client.SyncJoinedTo(alice.UserID, replacementRoom),
			client.SyncTimelineHas(replacementRoom, stateExists("m.room.create")),
			client.SyncTimelineHas(replacementRoom, stateExists("m.room.member")),
			client.SyncTimelineHas(replacementRoom, stateExists("m.room.power_levels")),
		)

		user.JoinRoom(t, replacementRoom, []string{"hs1"})
		user.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(user.UserID, replacementRoom))

		user.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "pushrules", ""}, client.WithRetryUntil(10*time.Second, func(resp *http.Response) bool {
			pushRuleJson := gjson.ParseBytes(must.ParseJSON(t, resp.Body))

			t.Logf(pushRuleJson.Get("global.rooms").Raw)
			t.Logf(roomID)

			replacementPushRule := pushRuleJson.Get("global.room.#(rule_id==\"" + client.GjsonEscape(replacementRoom) + "\").actions")

			if !replacementPushRule.Exists() {
				t.Logf("rule for %s did not exist, retrying...", replacementRoom)
				return false
			}

			if !reflect.DeepEqual(replacementPushRule.Value(), []interface{}{"notify"}) {
				t.Logf("rule actions did not match pre-upgrade rule, retrying...")
				return false
			} else {
				return true
			}
		}))
	}

	t.Run("local user gets push rules copied", func(t *testing.T) {
		doTestWith(t, bob)
	})

	t.Run("remote user gets push rules copied", func(t *testing.T) {
		doTestWith(t, charlie)
	})
}
