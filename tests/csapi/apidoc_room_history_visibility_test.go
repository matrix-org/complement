package csapi_tests

import (
	"github.com/matrix-org/complement/runtime"
	"net/http"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

// TODO most of this can be refactored into data-driven tests

func fetchEvent(t *testing.T, c *client.CSAPI, roomId, eventId string) *http.Response {
	return c.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomId, "event", eventId})
}

func createRoomWithVisibility(t *testing.T, c *client.CSAPI, visibility string) string {
	return c.CreateRoom(t, map[string]interface{}{
		"initial_state": []map[string]interface{}{
			{
				"content": map[string]interface{}{
					"history_visibility": visibility,
				},
				"type": "m.room.history_visibility",
			},
		},
		"preset": "public_chat",
	})
}

// Fetches an event after join, and succeeds.
// sytest: /event/ on joined room works
func TestFetchEvent(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := createRoomWithVisibility(t, alice, "shared")

	bob.JoinRoom(t, roomID, nil)

	// todo: replace with `SyncUntilJoined`
	bob.SyncUntilTimelineHas(t, roomID, func(event gjson.Result) bool {
		return event.Get("type").Str == "m.room.member" &&
			event.Get("content.membership").Str == "join" &&
			event.Get("state_key").Str == bob.UserID
	})

	eventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"body": "Hello world",
		},
	})

	res := fetchEvent(t, bob, roomID, eventID)

	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 200,
		JSON: []match.JSON{
			// No harm in checking if the event data is also as expected
			match.JSONKeyEqual("content", map[string]interface{}{
				"body": "Hello world",
			}),
			match.JSONKeyEqual("type", "m.room.message"),

			// the spec technically doesn't list these following keys, but we're still checking them because sytest did.
			// see: https://github.com/matrix-org/matrix-doc/issues/3540
			match.JSONKeyEqual("room_id", roomID),
			match.JSONKeyEqual("sender", alice.UserID),
			match.JSONKeyEqual("event_id", eventID),
			match.JSONKeyTypeEqual("origin_server_ts", gjson.Number),
		},
	})
}

// Tries to fetch an event before join, and fails.
// history_visibility: joined
// sytest: /event/ does not allow access to events before the user joined
func TestFetchHistoricalJoinedEventDenied(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := createRoomWithVisibility(t, alice, "joined")

	eventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"body": "Hello world",
		},
	})

	bob.JoinRoom(t, roomID, nil)

	// todo: replace with `SyncUntilJoined`
	bob.SyncUntilTimelineHas(t, roomID, func(event gjson.Result) bool {
		return event.Get("type").Str == "m.room.member" &&
			event.Get("content.membership").Str == "join" &&
			event.Get("state_key").Str == bob.UserID
	})

	res := fetchEvent(t, bob, roomID, eventID)

	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 404,
	})
}

// Tries to fetch an event before join, and succeeds.
// history_visibility: shared
func TestFetchHistoricalSharedEvent(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // FIXME https://github.com/matrix-org/dendrite/issues/617

	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := createRoomWithVisibility(t, alice, "shared")

	eventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"body": "Hello world",
		},
	})

	bob.JoinRoom(t, roomID, nil)

	// todo: replace with `SyncUntilJoined`
	bob.SyncUntilTimelineHas(t, roomID, func(event gjson.Result) bool {
		return event.Get("type").Str == "m.room.member" &&
			event.Get("content.membership").Str == "join" &&
			event.Get("state_key").Str == bob.UserID
	})

	res := fetchEvent(t, bob, roomID, eventID)

	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 200,
		JSON: []match.JSON{
			// No harm in checking if the event data is also as expected
			match.JSONKeyEqual("content", map[string]interface{}{
				"body": "Hello world",
			}),
			match.JSONKeyEqual("type", "m.room.message"),

			// the spec technically doesn't list these following keys, but we're still checking them because sytest did.
			// see: https://github.com/matrix-org/matrix-doc/issues/3540
			match.JSONKeyEqual("room_id", roomID),
			match.JSONKeyEqual("sender", alice.UserID),
			match.JSONKeyEqual("event_id", eventID),
			match.JSONKeyTypeEqual("origin_server_ts", gjson.Number),
		},
	})
}

// Tries to fetch an event between being invited and joined, and succeeds.
// history_visibility: invited
func TestFetchHistoricalInvitedEventFromBetweenInvite(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // FIXME https://github.com/matrix-org/dendrite/issues/617

	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := createRoomWithVisibility(t, alice, "invited")

	alice.InviteRoom(t, roomID, bob.UserID)

	bob.SyncUntilInvitedTo(t, roomID)

	eventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"body": "Hello world",
		},
	})

	bob.JoinRoom(t, roomID, nil)

	// todo: replace with `SyncUntilJoined`
	bob.SyncUntilTimelineHas(t, roomID, func(event gjson.Result) bool {
		return event.Get("type").Str == "m.room.member" &&
			event.Get("content.membership").Str == "join" &&
			event.Get("state_key").Str == bob.UserID
	})

	res := fetchEvent(t, bob, roomID, eventID)

	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 200,
		JSON: []match.JSON{
			// No harm in checking if the event data is also as expected
			match.JSONKeyEqual("content", map[string]interface{}{
				"body": "Hello world",
			}),
			match.JSONKeyEqual("type", "m.room.message"),

			// the spec technically doesn't list these following keys, but we're still checking them because sytest did.
			// see: https://github.com/matrix-org/matrix-doc/issues/3540
			match.JSONKeyEqual("room_id", roomID),
			match.JSONKeyEqual("sender", alice.UserID),
			match.JSONKeyEqual("event_id", eventID),
			match.JSONKeyTypeEqual("origin_server_ts", gjson.Number),
		},
	})
}

// Tries to fetch an event before being invited, and fails.
// history_visibility: invited
func TestFetchHistoricalInvitedEventFromBeforeInvite(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := createRoomWithVisibility(t, alice, "invited")

	eventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"body": "Hello world",
		},
	})

	alice.InviteRoom(t, roomID, bob.UserID)

	bob.SyncUntilInvitedTo(t, roomID)

	bob.JoinRoom(t, roomID, nil)

	// todo: replace with `SyncUntilJoined`
	bob.SyncUntilTimelineHas(t, roomID, func(event gjson.Result) bool {
		return event.Get("type").Str == "m.room.member" &&
			event.Get("content.membership").Str == "join" &&
			event.Get("state_key").Str == bob.UserID
	})

	res := fetchEvent(t, bob, roomID, eventID)

	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 404,
	})
}

// Tries to fetch an event without having joined, and fails.
// history_visibility: shared
// sytest: /event/ on non world readable room does not work
func TestFetchEventNonWorldReadable(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := createRoomWithVisibility(t, alice, "shared")

	eventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"body": "Hello world",
		},
	})

	res := fetchEvent(t, bob, roomID, eventID)

	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 404,
	})
}

// Tries to fetch an event without having joined, and succeeds.
// history_visibility: world_readable
func TestFetchEventWorldReadable(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // FIXME https://github.com/matrix-org/dendrite/issues/617

	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := createRoomWithVisibility(t, alice, "world_readable")

	eventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"body": "Hello world",
		},
	})

	res := fetchEvent(t, bob, roomID, eventID)

	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 200,
		JSON: []match.JSON{
			// No harm in checking if the event data is also as expected
			match.JSONKeyEqual("content", map[string]interface{}{
				"body": "Hello world",
			}),
			match.JSONKeyEqual("type", "m.room.message"),

			// the spec technically doesn't list these following keys, but we're still checking them because sytest did.
			// see: https://github.com/matrix-org/matrix-doc/issues/3540
			match.JSONKeyEqual("room_id", roomID),
			match.JSONKeyEqual("sender", alice.UserID),
			match.JSONKeyEqual("event_id", eventID),
			match.JSONKeyTypeEqual("origin_server_ts", gjson.Number),
		},
	})
}
