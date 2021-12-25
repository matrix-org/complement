package csapi_tests

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
)

func stringSliceContains(slice []string, s string) bool {
	for _, value := range slice {
		if value == s {
			return true
		}
	}
	return false
}

// sytest: Events come down the correct room
func TestEventsInCorrectRoom(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	const roomAmount = 30

	// Create all rooms

	var chanRooms = make(chan string, roomAmount)

	// semaphore, to reduce load on the server when creating rooms
	// on slow PCs, increasing this will time out the test :(
	var sem = make(chan bool, 1)

	for i := 0; i < roomAmount; i++ {
		sem <- true

		go func() {
			roomId := alice.CreateRoom(t, map[string]interface{}{
				"preset": "public_chat",
			})

			chanRooms <- roomId

			<-sem
		}()
	}

	var rooms []string

	for i := 0; i < roomAmount; i++ {
		// This implicitly waits until all rooms are done creating
		rooms = append(rooms, <-chanRooms)
	}

	// Send events to all rooms with corresponding roomID

	// get current next_batch
	_, since := alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

	wg := NewWaiterGroup(roomAmount)

	txnCount := 0

	for _, id := range rooms {
		go func(roomId string, txn int) {
			defer wg.Done()

			paths := []string{"_matrix", "client", "r0", "rooms", roomId, "send", "m.room.message", strconv.Itoa(txn)}
			alice.MustDoFunc(t, "PUT", paths, client.WithJSONBody(t, map[string]interface{}{
				"msgtype": "m.text",
				"body":    roomId,
			}))
		}(id, txnCount)

		txnCount++
	}

	wg.WaitAll(t, 10*time.Second)

	// Collect all events, check if room IDs check out

	okayRooms := make(map[string]bool)

	alice.MustSyncUntil(t, client.SyncReq{Since: since}, func(_ string, jsonObj gjson.Result) error {
		join := jsonObj.Get("rooms.join")

		if !join.Exists() {
			return fmt.Errorf("could not find rooms.join")
		}

		if !join.IsObject() {
			t.Fatalf("expected join in sync to be object, was %s; %s", join.Type.String(), join)
		}

		for roomID, room := range join.Map() {
			if !stringSliceContains(rooms, roomID) {
				// We're running an isolated test, so we shouldn't be getting sync from other rooms
				t.Fatalf("Got sync for unrelated room %s; %s", roomID, room)
			}

			eventsJson := room.Get("timeline.events")

			if !eventsJson.Exists() {
				continue
			}

			if !eventsJson.IsArray() {
				t.Fatalf("Expected events array in sync response to be an array: %s", eventsJson)
			}

			events := eventsJson.Array()

			if len(events) > 1 {
				t.Fatalf("Expected 0 or 1 events, got %d events: %s", len(events), events)
			} else if len(events) == 1 {
				if okayRooms[roomID] {
					// We have already cleared an event for this room?
					t.Fatalf("Event received for room %s where event was already received for: %s", roomID, events[0])
				}

				event := events[0]
				rawEvent := []byte(event.Raw)

				if err := match.JSONKeyEqual("type", "m.room.message")(rawEvent); err != nil {
					t.Fatalf("Got error when matching type on event: room = %s, err = %s, event = %s", roomID, err, event)
				}

				expectedEventContent := map[string]interface{}{
					"msgtype": "m.text",
					"body":    roomID,
				}

				if err := match.JSONKeyEqual("content", expectedEventContent)(rawEvent); err != nil {
					t.Fatalf("Got error when matching content on event: room = %s, err = %s, event = %s", roomID, err, event)
				}

				// Event passed
				okayRooms[roomID] = true
			} else {
				t.Logf("WARN: Sync struct got 0 events array in %s timeline??", roomID)
			}

		}

		if len(rooms) == len(okayRooms) {
			return nil
		} else {
			return fmt.Errorf("did not yet finish getting all rooms, need %d more rooms", len(rooms)-len(okayRooms))
		}
	})
}
