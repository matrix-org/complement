package csapi_tests

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/must"
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
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	userID := "@alice:hs1"
	alice := deployment.Client(t, "hs1", userID)

	// sytest: Room creation reports m.room.create to myself
	t.Run("Room creation reports m.room.create to myself", func(t *testing.T) {
		roomID := alice.CreateRoom(t, struct{}{})
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
			if ev.Get("type").Str != "m.room.create" {
				return false
			}
			must.EqualStr(t, ev.Get("sender").Str, userID, "wrong sender")
			must.EqualStr(t, ev.Get("content").Get("creator").Str, userID, "wrong content.creator")
			return true
		}))
	})
	// sytest: Room creation reports m.room.member to myself
	t.Run("Room creation reports m.room.member to myself", func(t *testing.T) {
		roomID := alice.CreateRoom(t, struct{}{})
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
			if ev.Get("type").Str != "m.room.member" {
				return false
			}
			must.EqualStr(t, ev.Get("sender").Str, userID, "wrong sender")
			must.EqualStr(t, ev.Get("state_key").Str, userID, "wrong state_key")
			must.EqualStr(t, ev.Get("content").Get("membership").Str, "join", "wrong content.membership")
			return true
		}))
	})
}
