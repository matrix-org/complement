package tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
)

// TODO:
// Setting room topic reports m.room.topic to myself
// Global initialSync
// Global initialSync with limit=0 gives no messages
// Room initialSync
// Room initialSync with limit=0 gives no messages
// Setting state twice is idempotent
// Joining room twice is idempotent

// Test that the m.room.create and m.room.member events for a room we created comes down /sync
func TestRoomCreationReportsEventsToMyself(t *testing.T) {
	deployment := Deploy(t, "rooms_state", b.BlueprintAlice)
	defer deployment.Destroy(t)

	userID := "@alice:hs1"
	alice := deployment.Client(t, "hs1", userID)
	res := alice.MustDo(t, "POST", []string{"_matrix", "client", "r0", "createRoom"}, struct{}{})
	body := must.ParseJSON(t, res.Body)
	roomID := must.GetJSONFieldStr(t, body, "room_id")

	t.Run("parallel", func(t *testing.T) {
		t.Run("reports m.room.create event", func(t *testing.T) {
			t.Parallel()
			alice := deployment.Client(t, "hs1", userID)
			alice.SyncUntilTimelineHas(t, roomID, func(ev gjson.Result) bool {
				if ev.Get("type").Str != "m.room.create" {
					return false
				}
				must.EqualStr(t, ev.Get("sender").Str, userID, "wrong sender")
				must.EqualStr(t, ev.Get("content").Get("creator").Str, userID, "wrong content.creator")
				return true
			})
		})
		t.Run("reports m.room.member event", func(t *testing.T) {
			t.Parallel()
			alice := deployment.Client(t, "hs1", userID)
			alice.SyncUntilTimelineHas(t, roomID, func(ev gjson.Result) bool {
				if ev.Get("type").Str != "m.room.member" {
					return false
				}
				must.EqualStr(t, ev.Get("sender").Str, userID, "wrong sender")
				must.EqualStr(t, ev.Get("state_key").Str, userID, "wrong state_key")
				must.EqualStr(t, ev.Get("content").Get("membership").Str, "join", "wrong content.membership")
				return true
			})
		})
	})
}
