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
// - SS1 is marked as a "space", but SS2 is not.
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
		"creation_content": map[string]interface{}{
			"org.matrix.msc1772.type": "org.matrix.msc1772.space",
		},
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
		roomRefs := map[string]int{
			root: 4, // r1,r2,ss1,parent r2
			r1:   1, // root
			r2:   2, // root,parent
			ss1:  3, // root,ss2,r3
			r3:   1, // ss1
			ss2:  2, // ss1,r4
			r4:   1, // ss2
		}
		res := alice.MustDo(t, "POST", []string{"_matrix", "client", "unstable", "rooms", root, "spaces"}, map[string]interface{}{})
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONCheckOff("rooms", []interface{}{
					root, r1, r2, r3, r4, ss1, ss2,
				}, func(r gjson.Result) interface{} {
					return r.Get("room_id").Str
				}, func(roomInt interface{}, data gjson.Result) error {
					roomID := roomInt.(string)
					// check fields
					if name, ok := roomNames[roomID]; ok {
						if data.Get("name").Str != name {
							return fmt.Errorf("room %s got name %s want %s", roomID, data.Get("name").Str, name)
						}
					}
					if refs, ok := roomRefs[roomID]; ok {
						gotRefs := data.Get("num_refs").Int()
						if int64(refs) != gotRefs {
							return fmt.Errorf("room %s got %d refs want %d", roomID, gotRefs, refs)
						}
					}
					if roomID == ss1 {
						wantType := "org.matrix.msc1772.space"
						if data.Get("room_type").Str != wantType {
							return fmt.Errorf("room %s got type %s want %s", roomID, data.Get("room_type").Str, wantType)
						}
					}
					return nil
				}),
				match.JSONCheckOff("events", []interface{}{
					rootToR1, rootToR2, rootToSS1, r2ToRoot,
					ss1ToSS2, r3ToSS1,
					ss2ToR4,
				}, func(r gjson.Result) interface{} {
					return r.Get("event_id").Str
				}, nil),
			},
		})
	})

	// TODO:
	// - Setting max_rooms_per_space works correctly
	t.Run("max_rooms_per_space", func(t *testing.T) {

	})

	/*
		// - Setting limit works correctly
		t.Run("limit", func(t *testing.T) {
			// should omit R4 due to limit
			res := alice.MustDo(t, "POST", []string{"_matrix", "client", "unstable", "rooms", root, "spaces"}, map[string]interface{}{
				"limit": 6,
			})
		}) */
}
