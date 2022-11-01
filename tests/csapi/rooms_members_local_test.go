package csapi_tests

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/matrix-org/complement/runtime"
)

func TestMembersLocal(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")
	roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})

	t.Run("Parallel", func(t *testing.T) {
		// sytest: New room members see their own join event
		t.Run("New room members see their own join event", func(t *testing.T) {
			t.Parallel()
			// SyncJoinedTo already checks everything we need to know
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
		})

		// sytest: Existing members see new members' join events
		t.Run("Existing members see new members' join events", func(t *testing.T) {
			t.Parallel()
			bob.JoinRoom(t, roomID, []string{})
			// SyncJoinedTo already checks everything we need to know
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
		})

		// sytest: Existing members see new members' presence
		t.Run("Existing members see new members' presence", func(t *testing.T) {
			runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/2803
			t.Parallel()
			alice.MustSyncUntil(t, client.SyncReq{},
				client.SyncJoinedTo(bob.UserID, roomID),
				func(clientUserID string, topLevelSyncJSON gjson.Result) error {
					presenceEvents := topLevelSyncJSON.Get("presence.events")
					if !presenceEvents.Exists() {
						return fmt.Errorf("presence.events does not exist")
					}
					for _, x := range presenceEvents.Array() {
						fieldsExists := x.Get("type").Exists() && x.Get("content").Exists() && x.Get("sender").Exists()
						if !fieldsExists {
							return fmt.Errorf("expected fields type, content and sender")
						}
						if x.Get("sender").Str == bob.UserID {
							return nil
						}
					}
					return fmt.Errorf("did not find %s in presence events", bob.UserID)
				},
			)
		})
	})

}

// sytest: Can get rooms/{roomId}/members at a given point
func TestMembersAtAGivenPoint(t *testing.T) {
	deplyoment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deplyoment.Destroy(t)

	alice := deplyoment.Client(t, "hs1", "@alice:hs1")
	bob := deplyoment.Client(t, "hs1", "@bob:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})
	alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello World!",
		},
	})

	resp, _ := alice.MustSync(t, client.SyncReq{})
	prevBatch := resp.Get("rooms.join." + client.GjsonEscape(roomID) + ".timeline.prev_batch").Str

	bob.JoinRoom(t, roomID, []string{})
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello back",
		},
	})

	params := url.Values{}
	params.Add("at", prevBatch)

	roomsResp := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "members"}, client.WithQueries(params))
	must.MatchResponse(t, roomsResp, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONCheckOff("chunk", []interface{}{
				alice.UserID,
			}, func(r gjson.Result) interface{} {
				return r.Get("state_key").Str
			}, nil),
		},
	})
}
