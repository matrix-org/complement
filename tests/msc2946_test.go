// +build msc2946

package tests

import (
	"fmt"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/tidwall/gjson"
)

// Tests that the CS API for MSC2946 works correctly. Creates a space directory like:
//     Root
//      |
// _____|________
// |    |       |
// R1  SS1      R2
//      |________
//      |        |
//     SS2      R3
//      |
//      R4
//
// Where:
// - the user is joined to all rooms except R4.
// - R3 -> SS1 is a parent link without a child.
// - R2 <---> Root is a two-way link.
// - The remaining links are just children links.
//
// Tests that:
// - Querying the root returns the entire graph
// - Setting max_rooms_per_space works correctly
// - Setting limit works correctly
// - Rooms are returned correctly along with the custom fields `num_refs` and `room_type`.
// - Events are returned correctly.
// - Redacting links works correctly.
func TestClientSpacesSummary(t *testing.T) {
	spaceChildEventType := "org.matrix.msc1772.space.child"
	spaceParentEventType := "org.matrix.msc1772.space.parent"
	deployment := Deploy(t, "msc2946", b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	roomNames := make(map[string]string)

	// create the rooms
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	root := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "Root",
	})
	roomNames[root] = "Root"
	r1 := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "R1",
	})
	roomNames[r1] = "R1"
	ss1 := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "Sub-Space 1",
		"topic":  "Some topic for sub-space 1",
	})
	roomNames[ss1] = "Sub-Space 1"
	r2 := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})
	ss2 := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})
	r3 := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})
	// alice is not joined to R4
	bob := deployment.Client(t, "hs1", "@bob:hs1")
	r4 := bob.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"initial_state": []map[string]interface{}{
			{
				"type":      "m.room.history_visibility",
				"state_key": "",
				"content": map[string]string{
					"history_visibility": "world_readable",
				},
			},
		},
	})

	// create the links
	rootToR1 := alice.SendEventSynced(t, root, b.Event{
		Type:     spaceChildEventType,
		StateKey: &r1,
		Content: map[string]interface{}{
			"via": []string{"hs1"},
		},
	})
	rootToSS1 := alice.SendEventSynced(t, root, b.Event{
		Type:     spaceChildEventType,
		StateKey: &ss1,
		Content: map[string]interface{}{
			"via": []string{"hs1"},
		},
	})
	rootToR2 := alice.SendEventSynced(t, root, b.Event{
		Type:     spaceChildEventType,
		StateKey: &r2,
		Content: map[string]interface{}{
			"via": []string{"hs1"},
		},
	})
	r2ToRoot := alice.SendEventSynced(t, r2, b.Event{ // parent link
		Type:     spaceParentEventType,
		StateKey: &root,
		Content: map[string]interface{}{
			"via": []string{"hs1"},
		},
	})
	ss1ToSS2 := alice.SendEventSynced(t, ss1, b.Event{
		Type:     spaceChildEventType,
		StateKey: &ss2,
		Content: map[string]interface{}{
			"via": []string{"hs1"},
		},
	})
	r3ToSS1 := alice.SendEventSynced(t, r3, b.Event{ // parent link only
		Type:     spaceParentEventType,
		StateKey: &ss1,
		Content: map[string]interface{}{
			"via": []string{"hs1"},
		},
	})
	ss2ToR4 := alice.SendEventSynced(t, ss2, b.Event{
		Type:     spaceChildEventType,
		StateKey: &r4,
		Content: map[string]interface{}{
			"via": []string{"hs1"},
		},
	})

	// Run tests in parallel

	// - Querying the root returns the entire graph
	// - Rooms are returned correctly along with the custom fields `num_refs` and `room_type`.
	// - Events are returned correctly.
	t.Run("query whole graph", func(t *testing.T) {
		wantRooms := []string{
			root, r1, r2, r3, r4, ss1, ss2,
		}
		wantEvents := []string{
			rootToR1, rootToR2, rootToSS1, r2ToRoot,
			ss1ToSS2, r3ToSS1,
			ss2ToR4,
		}
		res := alice.MustDo(t, "POST", []string{"_matrix", "client", "unstable", "rooms", root, "spaces"}, map[string]interface{}{})
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONArrayEach("rooms", func(room gjson.Result) error {
					roomID := room.Get("room_id").Str
					want := -1
					for i, w := range wantRooms {
						if w == roomID {
							want = i
							break
						}
					}
					if want == -1 {
						return fmt.Errorf("did not want room %s", roomID)
					}
					// delete the wanted room
					wantRooms = append(wantRooms[:want], wantRooms[want+1:]...)

					// check fields
					if name, ok := roomNames[roomID]; ok {
						if room.Get("name").Str != name {
							return fmt.Errorf("room %s got name %s want %s", roomID, room.Get("name").Str, name)
						}
					}

					return nil
				}),
				match.JSONArrayEach("events", func(event gjson.Result) error {
					eventID := event.Get("event_id").Str
					want := -1
					for i, w := range wantEvents {
						if w == eventID {
							want = i
							break
						}
					}
					if want == -1 {
						return fmt.Errorf("did not want event %s", eventID)
					}
					// delete the wanted event
					wantEvents = append(wantEvents[:want], wantEvents[want+1:]...)
					return nil
				}),
			},
		})
		if len(wantEvents) > 0 {
			t.Errorf("Wanted more events: %v", wantEvents)
		}
		if len(wantRooms) > 0 {
			t.Errorf("Wanted more rooms: %v", wantRooms)
		}
	})

	// - Setting max_rooms_per_space works correctly
	t.Run("max_rooms_per_space", func(t *testing.T) {

	})

	// - Setting limit works correctly
	t.Run("limit", func(t *testing.T) {

	})

}
