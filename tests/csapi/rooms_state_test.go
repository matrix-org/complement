//go:build !dendrite_blacklist
// +build !dendrite_blacklist

package csapi_tests

import (
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/must"
)

func TestRoomCreationReportsEventsToMyself(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	userID := "@alice:hs1"
	alice := deployment.Client(t, "hs1", userID)
	bob := deployment.RegisterUser(t, "hs1", "bob", "bobpassword", false)
	roomID := alice.MustCreateRoom(t, struct{}{})

	t.Run("parallel", func(t *testing.T) {
		// sytest: Room creation reports m.room.create to myself
		t.Run("Room creation reports m.room.create to myself", func(t *testing.T) {
			t.Parallel()

			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
				if ev.Get("type").Str != "m.room.create" {
					return false
				}
				must.Equal(t, ev.Get("sender").Str, userID, "wrong sender")
				must.Equal(t, ev.Get("content").Get("creator").Str, userID, "wrong content.creator")
				return true
			}))
		})

		// sytest: Room creation reports m.room.member to myself
		t.Run("Room creation reports m.room.member to myself", func(t *testing.T) {
			t.Parallel()

			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
				if ev.Get("type").Str != "m.room.member" {
					return false
				}
				must.Equal(t, ev.Get("sender").Str, userID, "wrong sender")
				must.Equal(t, ev.Get("state_key").Str, userID, "wrong state_key")
				must.Equal(t, ev.Get("content").Get("membership").Str, "join", "wrong content.membership")
				return true
			}))
		})

		// sytest: Setting room topic reports m.room.topic to myself
		t.Run("Setting room topic reports m.room.topic to myself", func(t *testing.T) {
			t.Parallel()

			const roomTopic = "Testing topic for the new room"

			alice.SendEventSynced(t, roomID, b.Event{
				Type:     "m.room.topic",
				StateKey: b.Ptr(""),
				Content: map[string]interface{}{
					"topic": roomTopic,
				},
			})

			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
				if ev.Get("type").Str != "m.room.topic" {
					return false
				}
				if !ev.Get("state_key").Exists() {
					return false
				}
				must.Equal(t, ev.Get("sender").Str, userID, "wrong sender")
				must.Equal(t, ev.Get("content").Get("topic").Str, roomTopic, "wrong content.topic")
				return true
			}))
		})

		// sytest: Setting state twice is idempotent
		t.Run("Setting state twice is idempotent", func(t *testing.T) {
			t.Parallel()

			stateEvent := b.Event{
				Type:     "a.test.state.type",
				StateKey: b.Ptr(""),
				Content: map[string]interface{}{
					"a_key": "a_value",
				},
			}

			firstID := alice.SendEventSynced(t, roomID, stateEvent)
			secondID := alice.SendEventSynced(t, roomID, stateEvent)

			if firstID != secondID {
				t.Fatalf("Both Event IDs from supposedly-idempotent state-setting differ, %s != %s", firstID, secondID)
			}
		})

		// sytest: Joining room twice is idempotent
		t.Run("Joining room twice is idempotent", func(t *testing.T) {
			t.Parallel()

			roomID := bob.MustCreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
			})

			alice.MustJoinRoom(t, roomID, nil)
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

			firstID := *getEventIdForState(t, alice, roomID, "m.room.member", alice.UserID)

			alice.MustJoinRoom(t, roomID, nil)

			// Unfortunately there is no way to definitively wait
			// for a 'potentially false second join event' without
			// also failing the test on timeout, with the current APIs.
			//
			// So we take a conservative estimate of a 5-second sleep delay
			// before we check on the server again.
			time.Sleep(5 * time.Second)

			secondID := *getEventIdForState(t, alice, roomID, "m.room.member", alice.UserID)

			if firstID != secondID {
				t.Fatalf("Both Event IDs from supposedly-idempotent room joins differ, %s != %s", firstID, secondID)
			}
		})
	})
}

func getEventIdForState(t *testing.T, client *client.CSAPI, roomID, evType, stateKey string) *string {
	res := client.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state"})

	result := must.ParseJSON(t, res.Body)

	for _, ev := range result.Array() {
		if ev.Get("type").Str == evType && ev.Get("state_key").Str == stateKey {
			return b.Ptr(ev.Get("event_id").Str)
		}
	}

	return nil
}
