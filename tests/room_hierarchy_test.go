// This file includes tests for MSC2946, the spaces summary API.
//
// There are currently tests for two unstable versions of it for backwards
// compatibility:
//
// * The /spaces endpoint, which was the original version.
// * The /hierarchy endpoint, which is an updated version.
//
// Both endpoints return data from the same set of rooms / spaces, but have
// different API shapes.
//
// TODO When support for this is stable, the tests for /spaces should be removed.

package tests

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/gomatrixserverlib/spec"
)

var (
	spaceChildEventType  = "m.space.child"
	spaceParentEventType = "m.space.parent"
)

// the API doesn't return event IDs so we need to key off the
// 3-uple of room ID, event type and state key
func eventKey(srcRoomID, dstRoomID, evType string) string {
	return srcRoomID + "|" + dstRoomID + "|" + evType
}

// Shared mapper function to return a structure comparison string for JSONCheckOff.
func roomToChildrenMapper(r gjson.Result) interface{} {
	roomId := r.Get("room_id").Str

	result := ""
	for i, res := range r.Get("children_state").Array() {
		if i != 0 {
			result += ";"
		}
		result += eventKey(roomId, res.Get("state_key").Str, res.Get("type").Str)
	}

	return result
}

// Tests that the CS API for MSC2946 works correctly. Creates a space directory like:
//
//	Root
//	 |
//
// _____|________
// |    |       |
// R1  SS1      R2
//
//	 |       |
//	SS2      R5
//	 |________
//	 |       |
//	 R3      R4
//
// Where:
// - the user is joined to all rooms except R4.
// - R2 <---> Root is a two-way link.
// - The remaining links are just children links.
// - R1 and R2 are suggested rooms.
//
// Tests that:
// - Querying the root returns the entire graph
// - Setting max_rooms_per_space works correctly
// - Setting limit works correctly
// - Rooms are returned correctly along with the custom fields `room_type`.
// - Events are returned correctly.
// - Redacting links works correctly.
func TestClientSpacesSummary(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	roomNames := make(map[string]string)

	// create the rooms
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	root := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "Root",
		"creation_content": map[string]interface{}{
			"type": "m.space",
		},
	})
	roomNames[root] = "Root"
	r1 := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "R1",
	})
	roomNames[r1] = "R1"
	ss1 := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "Sub-Space 1",
		"topic":  "Some topic for sub-space 1",
		"creation_content": map[string]interface{}{
			"type": "m.space",
		},
	})
	roomNames[ss1] = "Sub-Space 1"
	r2 := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "R2",
	})
	roomNames[r2] = "R2"
	ss2 := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "SS2",
		"creation_content": map[string]interface{}{
			"type": "m.space",
		},
	})
	roomNames[ss2] = "SS2"
	r3 := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "R3",
	})
	roomNames[r3] = "R3"
	// alice is not joined to R4
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	r4 := bob.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "R4",
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
	roomNames[r4] = "R4"
	r5 := bob.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "R5",
	})
	roomNames[r5] = "R5"
	t.Logf("%+v", roomNames)

	// create the links
	rootToR1 := eventKey(root, r1, spaceChildEventType)
	alice.SendEventSynced(t, root, b.Event{
		Type:     spaceChildEventType,
		StateKey: &r1,
		Content: map[string]interface{}{
			"via":       []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs1"))},
			"suggested": true,
		},
	})
	rootToSS1 := eventKey(root, ss1, spaceChildEventType)
	alice.SendEventSynced(t, root, b.Event{
		Type:     spaceChildEventType,
		StateKey: &ss1,
		Content: map[string]interface{}{
			"via": []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs1"))},
		},
	})
	rootToR2 := eventKey(root, r2, spaceChildEventType)
	alice.SendEventSynced(t, root, b.Event{
		Type:     spaceChildEventType,
		StateKey: &r2,
		Content: map[string]interface{}{
			"via":       []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs1"))},
			"suggested": true,
		},
	})
	// Note that this link gets ignored since R2 is not a space.
	alice.SendEventSynced(t, r2, b.Event{
		Type:     spaceChildEventType,
		StateKey: &r5,
		Content: map[string]interface{}{
			"via": []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs1"))},
		},
	})
	alice.SendEventSynced(t, r2, b.Event{ // parent link
		Type:     spaceParentEventType,
		StateKey: &root,
		Content: map[string]interface{}{
			"via": []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs1"))},
		},
	})
	ss1ToSS2 := eventKey(ss1, ss2, spaceChildEventType)
	alice.SendEventSynced(t, ss1, b.Event{
		Type:     spaceChildEventType,
		StateKey: &ss2,
		Content: map[string]interface{}{
			"via": []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs1"))},
		},
	})
	ss2ToR3 := eventKey(ss2, r3, spaceChildEventType)
	alice.SendEventSynced(t, ss2, b.Event{
		Type:     spaceChildEventType,
		StateKey: &r3,
		Content: map[string]interface{}{
			"via": []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs1"))},
		},
	})
	ss2ToR4 := eventKey(ss2, r4, spaceChildEventType)
	alice.SendEventSynced(t, ss2, b.Event{
		Type:     spaceChildEventType,
		StateKey: &r4,
		Content: map[string]interface{}{
			"via": []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs1"))},
		},
	})

	// - Querying the root returns the entire graph
	// - Rooms are returned correctly along with the custom fields `room_type`.
	// - Events are returned correctly.
	t.Run("query whole graph", func(t *testing.T) {
		res := alice.MustDo(t, "GET", []string{"_matrix", "client", "v1", "rooms", root, "hierarchy"})
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONCheckOffDeprecated("rooms", []interface{}{
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
					if roomID == ss1 {
						wantType := "m.space"
						if data.Get("room_type").Str != wantType {
							return fmt.Errorf("room %s got type %s want %s", roomID, data.Get("room_type").Str, wantType)
						}
					}
					return nil
				}),
				// Check that the links from Root down to other rooms and spaces exist.
				match.JSONCheckOffDeprecated(`rooms.#(room_type=="m.space")#")`, []interface{}{
					rootToR1 + ";" + rootToSS1 + ";" + rootToR2,
					ss1ToSS2,
					ss2ToR3 + ";" + ss2ToR4,
				}, roomToChildrenMapper, nil),
			},
		})
	})

	// - Setting max_depth works correctly
	t.Run("max_depth", func(t *testing.T) {
		// Should only include R1, SS1, and R2.
		query := make(url.Values, 1)
		query.Set("max_depth", "1")
		res := alice.MustDo(
			t,
			"GET",
			[]string{"_matrix", "client", "v1", "rooms", root, "hierarchy"},
			client.WithQueries(query),
		)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONCheckOffDeprecated("rooms", []interface{}{
					root, r1, r2, ss1,
				}, func(r gjson.Result) interface{} {
					return r.Get("room_id").Str
				}, nil),
				// All of the links are still there.
				match.JSONCheckOffDeprecated(`rooms.#(room_type=="m.space")#`, []interface{}{
					rootToR1 + ";" + rootToSS1 + ";" + rootToR2, ss1ToSS2,
				}, roomToChildrenMapper, nil),
			},
		})
	})

	// - Setting suggested_only works correctly
	t.Run("suggested_only", func(t *testing.T) {
		// Should only include R1, SS1, and R2.
		query := make(url.Values, 1)
		query.Set("suggested_only", "true")
		res := alice.MustDo(
			t,
			"GET",
			[]string{"_matrix", "client", "v1", "rooms", root, "hierarchy"},
			client.WithQueries(query),
		)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONCheckOffDeprecated("rooms", []interface{}{
					root, r1, r2,
				}, func(r gjson.Result) interface{} {
					return r.Get("room_id").Str
				}, nil),
				// All of the links are still there.
				match.JSONCheckOffDeprecated(`rooms.#(room_type=="m.space")#`, []interface{}{
					rootToR1 + ";" + rootToR2,
				}, roomToChildrenMapper, nil),
			},
		})
	})

	// - Setting max_depth works correctly
	t.Run("pagination", func(t *testing.T) {
		// The initial page should only include Root, R1, SS1, and SS2.
		query := make(url.Values, 1)
		query.Set("limit", "4")
		res := alice.MustDo(
			t,
			"GET",
			[]string{"_matrix", "client", "v1", "rooms", root, "hierarchy"},
			client.WithQueries(query),
		)
		body := must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONCheckOffDeprecated("rooms", []interface{}{
					root, r1, ss1, ss2,
				}, func(r gjson.Result) interface{} {
					return r.Get("room_id").Str
				}, nil),
			},
		})

		// The following page should include R3, R4, and R2.
		query = make(url.Values, 1)
		query.Set("from", client.GetJSONFieldStr(t, body, "next_batch"))
		res = alice.MustDo(
			t,
			"GET",
			[]string{"_matrix", "client", "v1", "rooms", root, "hierarchy"},
			client.WithQueries(query),
		)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONCheckOffDeprecated("rooms", []interface{}{
					r3, r4, r2,
				}, func(r gjson.Result) interface{} {
					return r.Get("room_id").Str
				}, nil),
			},
		})
	})

	t.Run("redact link", func(t *testing.T) {
		// Remove the root -> SS1 link
		alice.SendEventSynced(t, root, b.Event{
			Type:     spaceChildEventType,
			StateKey: &ss1,
			Content:  map[string]interface{}{},
		})
		res := alice.MustDo(t, "GET", []string{"_matrix", "client", "v1", "rooms", root, "hierarchy"})
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONCheckOffDeprecated("rooms", []interface{}{
					root, r1, r2,
				}, func(r gjson.Result) interface{} {
					return r.Get("room_id").Str
				}, nil),
				match.JSONCheckOffDeprecated(`rooms.#(room_type=="m.space")#`, []interface{}{
					rootToR1 + ";" + rootToR2,
				}, roomToChildrenMapper, nil),
			},
		})
	})
}

// Tests that the CS API for MSC2946 correctly handles join rules. Creates a space directory like:
//
//	Root
//	 |
//
// _____|
// |    |
// R1  SS1
//
//	|________
//	|       |
//	R2      R3
//
// Where:
// - All rooms and spaces are invite-only, except R2 (which is public)
// - The links are just children links.
//
// Tests that:
// - Rooms/spaces the user is not invited to should not appear.
func TestClientSpacesSummaryJoinRules(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	// create the rooms
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	root := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "Root",
		"creation_content": map[string]interface{}{
			"type": "m.space",
		},
	})
	r1 := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "private_chat",
		"name":   "R1",
	})
	ss1 := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "private_chat",
		"name":   "Sub-Space 1",
		"creation_content": map[string]interface{}{
			"type": "m.space",
		},
	})
	r2 := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "R2",
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
	r3 := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "private_chat",
		"name":   "R3",
	})

	// create the links
	rootToR1 := eventKey(root, r1, spaceChildEventType)
	alice.SendEventSynced(t, root, b.Event{
		Type:     spaceChildEventType,
		StateKey: &r1,
		Content: map[string]interface{}{
			"via": []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs1"))},
		},
	})
	rootToSS1 := eventKey(root, ss1, spaceChildEventType)
	alice.SendEventSynced(t, root, b.Event{
		Type:     spaceChildEventType,
		StateKey: &ss1,
		Content: map[string]interface{}{
			"via": []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs1"))},
		},
	})
	ss1ToR2 := eventKey(ss1, r2, spaceChildEventType)
	alice.SendEventSynced(t, ss1, b.Event{
		Type:     spaceChildEventType,
		StateKey: &r2,
		Content: map[string]interface{}{
			"via": []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs1"))},
		},
	})
	ss1ToR3 := eventKey(ss1, r3, spaceChildEventType)
	alice.SendEventSynced(t, ss1, b.Event{
		Type:     spaceChildEventType,
		StateKey: &r3,
		Content: map[string]interface{}{
			"via": []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs1"))},
		},
	})

	// Querying is done by bob who is not yet in any of the rooms.
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob.MustJoinRoom(t, root, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
	})

	res := bob.MustDo(t, "GET", []string{"_matrix", "client", "v1", "rooms", root, "hierarchy"})
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONCheckOffDeprecated("rooms", []interface{}{
				root,
			}, func(r gjson.Result) interface{} {
				return r.Get("room_id").Str
			}, nil),
			match.JSONCheckOffDeprecated(`rooms.#(room_type=="m.space")#`, []interface{}{
				rootToR1 + ";" + rootToSS1,
			}, roomToChildrenMapper, nil),
		},
	})

	// Invite to R1 and R3, querying again should only show R1 (since SS1 is not visible).
	alice.MustInviteRoom(t, r1, bob.UserID)
	alice.MustInviteRoom(t, r3, bob.UserID)

	res = bob.MustDo(t, "GET", []string{"_matrix", "client", "v1", "rooms", root, "hierarchy"})
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONCheckOffDeprecated("rooms", []interface{}{
				root, r1,
			}, func(r gjson.Result) interface{} {
				return r.Get("room_id").Str
			}, nil),
			match.JSONCheckOffDeprecated(`rooms.#(room_type=="m.space")#`, []interface{}{
				rootToR1 + ";" + rootToSS1,
			}, roomToChildrenMapper, nil),
		},
	})

	// Invite to SS1 and it now appears, as well as the rooms under it.
	alice.MustInviteRoom(t, ss1, bob.UserID)

	res = bob.MustDo(t, "GET", []string{"_matrix", "client", "v1", "rooms", root, "hierarchy"})
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONCheckOffDeprecated("rooms", []interface{}{
				root, r1, ss1, r2, r3,
			}, func(r gjson.Result) interface{} {
				return r.Get("room_id").Str
			}, nil),
			match.JSONCheckOffDeprecated(`rooms.#(room_type=="m.space")#`, []interface{}{
				rootToR1 + ";" + rootToSS1, ss1ToR2 + ";" + ss1ToR3,
			}, roomToChildrenMapper, nil),
		},
	})
}

// Tests that MSC2946 works over federation. Creates a space directory like:
//
//	ROOT
//	 |
//
// _____|________
// |    |       |
// R1  SS1      r2
//
//	 |________
//	 |        |
//	ss2      r3
//	 |
//	 R4
//
// Where R/SS = on hs1, and r/ss = on hs2. Links are space children state events only.
// Tests that:
// - Querying from root returns the entire graph
func TestFederatedClientSpaces(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	worldReadable := map[string]interface{}{
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
	}
	worldReadableSpace := map[string]interface{}{
		"preset": "public_chat",
		"creation_content": map[string]interface{}{
			"type": "m.space",
		},
		"initial_state": []map[string]interface{}{
			{
				"type":      "m.room.history_visibility",
				"state_key": "",
				"content": map[string]string{
					"history_visibility": "world_readable",
				},
			},
		},
	}
	// create the rooms
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	root := alice.MustCreateRoom(t, worldReadableSpace)
	r1 := alice.MustCreateRoom(t, worldReadable)
	ss1 := alice.MustCreateRoom(t, worldReadableSpace)
	r4 := alice.MustCreateRoom(t, worldReadable)
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})
	r2 := bob.MustCreateRoom(t, worldReadable)
	ss2 := bob.MustCreateRoom(t, worldReadableSpace)
	r3 := bob.MustCreateRoom(t, worldReadable)

	// create the links
	rootToR1 := eventKey(root, r1, spaceChildEventType)
	alice.SendEventSynced(t, root, b.Event{
		Type:     spaceChildEventType,
		StateKey: &r1,
		Content: map[string]interface{}{
			"via": []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs1"))},
		},
	})
	rootToSS1 := eventKey(root, ss1, spaceChildEventType)
	alice.SendEventSynced(t, root, b.Event{
		Type:     spaceChildEventType,
		StateKey: &ss1,
		Content: map[string]interface{}{
			"via": []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs1"))},
		},
	})
	rootToR2 := eventKey(root, r2, spaceChildEventType)
	alice.SendEventSynced(t, root, b.Event{
		Type:     spaceChildEventType,
		StateKey: &r2,
		Content: map[string]interface{}{
			"via": []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs2"))},
		},
	})
	ss1ToSS2 := eventKey(ss1, ss2, spaceChildEventType)
	alice.SendEventSynced(t, ss1, b.Event{
		Type:     spaceChildEventType,
		StateKey: &ss2,
		Content: map[string]interface{}{
			"via": []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs2"))},
		},
	})
	ss1ToR3 := eventKey(ss1, r3, spaceChildEventType)
	alice.SendEventSynced(t, ss1, b.Event{
		Type:     spaceChildEventType,
		StateKey: &r3,
		Content: map[string]interface{}{
			"via": []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs2"))},
		},
	})
	ss2ToR4 := eventKey(ss2, r4, spaceChildEventType)
	bob.SendEventSynced(t, ss2, b.Event{
		Type:     spaceChildEventType,
		StateKey: &r4,
		Content: map[string]interface{}{
			"via": []string{string(deployment.GetFullyQualifiedHomeserverName(t, "hs1"))},
		},
	})
	allEvents := []string{
		rootToR1, rootToR2, rootToSS1,
		ss1ToR3, ss1ToSS2,
		ss2ToR4,
	}
	t.Logf("rooms: %v", allEvents)

	res := alice.MustDo(t, "GET", []string{"_matrix", "client", "v1", "rooms", root, "hierarchy"})
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONCheckOffDeprecated("rooms", []interface{}{
				root, r1, r2, r3, r4, ss1, ss2,
			}, func(r gjson.Result) interface{} {
				return r.Get("room_id").Str
			}, nil),
		},
	})
}
