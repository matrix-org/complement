package csapi_tests

import (
	"net/http"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

// TODO most of this can be refactored into data-driven tests

func fetchEvent(t *testing.T, c *client.CSAPI, roomId, eventId string) *http.Response {
	return c.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomId, "event", eventId})
}

func createRoomWithVisibility(t *testing.T, c *client.CSAPI, visibility string) string {
	return c.MustCreateRoom(t, map[string]interface{}{
		"initial_state": []map[string]interface{}{
			{
				"content": map[string]interface{}{
					"history_visibility": visibility,
				},
				"type":      "m.room.history_visibility",
				"state_key": "",
			},
		},
		"preset": "public_chat",
	})
}

// Fetches an event after join, and succeeds.
// sytest: /event/ on joined room works
func TestFetchEvent(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := createRoomWithVisibility(t, alice, "shared")

	bob.MustJoinRoom(t, roomID, nil)

	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	eventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world",
		},
	})

	res := fetchEvent(t, bob, roomID, eventID)

	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 200,
		JSON: []match.JSON{
			// No harm in checking if the event data is also as expected
			match.JSONKeyEqual("content", map[string]interface{}{
				"msgtype": "m.text",
				"body":    "Hello world",
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
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := createRoomWithVisibility(t, alice, "joined")

	eventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world",
		},
	})

	bob.MustJoinRoom(t, roomID, nil)
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	res := fetchEvent(t, bob, roomID, eventID)

	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 404,
	})
}

// Tries to fetch an event before join, and succeeds.
// history_visibility: shared
func TestFetchHistoricalSharedEvent(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := createRoomWithVisibility(t, alice, "shared")

	eventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world",
		},
	})

	bob.MustJoinRoom(t, roomID, nil)
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	res := fetchEvent(t, bob, roomID, eventID)

	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 200,
		JSON: []match.JSON{
			// No harm in checking if the event data is also as expected
			match.JSONKeyEqual("content", map[string]interface{}{
				"msgtype": "m.text",
				"body":    "Hello world",
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
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := createRoomWithVisibility(t, alice, "invited")

	alice.MustInviteRoom(t, roomID, bob.UserID)
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))

	eventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world",
		},
	})

	bob.MustJoinRoom(t, roomID, nil)
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	res := fetchEvent(t, bob, roomID, eventID)

	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 200,
		JSON: []match.JSON{
			// No harm in checking if the event data is also as expected
			match.JSONKeyEqual("content", map[string]interface{}{
				"msgtype": "m.text",
				"body":    "Hello world",
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
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := createRoomWithVisibility(t, alice, "invited")

	eventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world",
		},
	})

	alice.MustInviteRoom(t, roomID, bob.UserID)
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))

	bob.MustJoinRoom(t, roomID, nil)
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	res := fetchEvent(t, bob, roomID, eventID)

	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 404,
	})
}

// Tries to fetch an event without having joined, and fails.
// history_visibility: shared
// sytest: /event/ on non world readable room does not work
func TestFetchEventNonWorldReadable(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := createRoomWithVisibility(t, alice, "shared")

	eventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world",
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
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := createRoomWithVisibility(t, alice, "world_readable")

	eventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world",
		},
	})

	res := fetchEvent(t, bob, roomID, eventID)

	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 200,
		JSON: []match.JSON{
			// No harm in checking if the event data is also as expected
			match.JSONKeyEqual("content", map[string]interface{}{
				"msgtype": "m.text",
				"body":    "Hello world",
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
