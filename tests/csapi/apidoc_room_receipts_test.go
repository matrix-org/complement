package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/tidwall/gjson"
)

// tests/10apidoc/37room-receipts.pl
func TestRoomReceiptsReadMarkers(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	roomID := alice.CreateRoom(t, map[string]interface{}{"preset": "public_chat"})

	// sytest: POST /rooms/:room_id/receipt can create receipts
	t.Run("Parallel", func(t *testing.T) {
		t.Run("POST /rooms/:room_id/receipt can create receipts", func(t *testing.T) {
			t.Parallel()
			eventID := ""
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(result gjson.Result) bool {
				if result.Get("type").Str == "m.room.member" {
					eventID = result.Get("event_id").Str
					return true
				}
				return false
			}))
			if eventID == "" {
				t.Fatal("did not find an event_id")
			}
			alice.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "receipt", "m.read", eventID}, client.WithJSONBody(t, struct{}{}))
		})

		// sytest: POST /rooms/:room_id/read_markers can create read marker
		t.Run("POST /rooms/:room_id/read_markers can create read marker", func(t *testing.T) {
			t.Parallel()
			eventID := ""
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(result gjson.Result) bool {
				if result.Get("type").Str == "m.room.member" {
					eventID = result.Get("event_id").Str
					return true
				}
				return false
			}))
			if eventID == "" {
				t.Fatal("did not find an event_id")
			}

			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"m.fully_read": eventID,
				"m.read":       eventID,
			})
			alice.MustDoFunc(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "read_markers"}, reqBody)
		})
	})
}
