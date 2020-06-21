package tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
)

func TestRoomCreationReportsMRoomCreateEventToMyself(t *testing.T) {
	deployment := must.Deploy(t, "rooms_state", b.BlueprintAlice.Name)
	defer deployment.Destroy(t)

	userID := "@alice:hs1"
	alice := deployment.Client(t, "hs1", userID)
	res := alice.MustDo(t, "POST", []string{"_matrix", "client", "r0", "createRoom"}, struct{}{})
	body := must.ParseJSON(t, res)
	roomID := must.GetJSONFieldStr(t, body, "room_id")
	alice.SyncUntilTimelineHas(t, roomID, func(ev gjson.Result) bool {
		if ev.Get("type").Str != "m.room.create" {
			return false
		}
		must.EqualStr(t, ev.Get("sender").Str, userID, "wrong sender")
		must.EqualStr(t, ev.Get("content").Get("creator").Str, userID, "wrong content.creator")
		return true
	})
}
