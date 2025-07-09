package csapi_tests

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

func TestRoomCreate(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	t.Run("Parallel", func(t *testing.T) {
		// sytest: POST /createRoom makes a public room
		t.Run("POST /createRoom makes a public room", func(t *testing.T) {
			t.Parallel()
			roomAlias := "30-room-create-alias-random"

			res := alice.CreateRoom(t, map[string]interface{}{
				"visibility":      "public",
				"room_alias_name": roomAlias,
			})
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("room_id", gjson.String),
				},
			})
		})
		// sytest: POST /createRoom makes a private room
		t.Run("POST /createRoom makes a private room", func(t *testing.T) {
			t.Parallel()

			res := alice.CreateRoom(t, map[string]interface{}{
				"visibility": "private",
			})
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("room_id", gjson.String),
				},
			})
		})
		// sytest: POST /createRoom makes a room with a topic
		t.Run("POST /createRoom makes a room with a topic", func(t *testing.T) {
			t.Parallel()

			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"topic":  "Test Room",
				"preset": "public_chat",
			})
			content := alice.MustGetStateEventContent(t, roomID, "m.room.topic", "")
			must.MatchGJSON(t, content, match.JSONKeyEqual("topic", "Test Room"))

			// The plain text topic is duplicated into m.topic
			must.MatchGJSON(t, content,
				match.JSONKeyArrayOfSize("m\\.topic.m\\.text", 1),
				match.JSONKeyPresent("m\\.topic.m\\.text.0.body"),
				match.JSONKeyEqual("m\\.topic.m\\.text.0.body", "Test Room"))

			// The mime type must be unset or text/plain
			mime := content.Get("m\\.topic.m\\.text.0.mimetype")
			if mime.Exists() {
				must.Equal(t, mime.String(), "text/plain", "no m.topic content block")
			}
		})
		// POST /createRoom makes a room with a topic via initial_state
		t.Run("POST /createRoom makes a room with a topic via initial_state", func(t *testing.T) {
			t.Parallel()

			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"initial_state": []map[string]interface{}{
					{
						"content": map[string]interface{}{
							"topic": "Test Room",
						},
						"type":      "m.room.topic",
						"state_key": "",
					},
				},
				"preset": "public_chat",
			})
			content := alice.MustGetStateEventContent(t, roomID, "m.room.topic", "")
			must.MatchGJSON(t, content, match.JSONKeyEqual("topic", "Test Room"))

			// There is no m.topic property
			must.MatchGJSON(t, content, match.JSONKeyMissing("m\\.topic"))
		})
		// POST /createRoom makes a room with a topic via initial_state overwritten by topic
		t.Run("POST /createRoom makes a room with a topic via initial_state overwritten by topic", func(t *testing.T) {
			t.Parallel()

			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"topic": "Test Room",
				"initial_state": []map[string]interface{}{
					{
						"content": map[string]interface{}{
							"topic": "Shenanigans",
						},
						"type":      "m.room.topic",
						"state_key": "",
					},
				},
				"preset": "public_chat",
			})
			content := alice.MustGetStateEventContent(t, roomID, "m.room.topic", "")
			must.MatchGJSON(t, content, match.JSONKeyEqual("topic", "Test Room"))

			// The plain text topic is duplicated into m.topic
			must.MatchGJSON(t, content,
				match.JSONKeyArrayOfSize("m\\.topic.m\\.text", 1),
				match.JSONKeyPresent("m\\.topic.m\\.text.0.body"),
				match.JSONKeyEqual("m\\.topic.m\\.text.0.body", "Test Room"))

			// The mime type must be unset or text/plain
			mime := content.Get("m\\.topic.m\\.text.0.mimetype")
			if mime.Exists() {
				must.Equal(t, mime.String(), "text/plain", "no m.topic content block")
			}
		})
		// sytest: POST /createRoom makes a room with a name
		t.Run("POST /createRoom makes a room with a name", func(t *testing.T) {
			t.Parallel()
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"name":   "Test Room",
				"preset": "public_chat",
			})
			content := alice.MustGetStateEventContent(t, roomID, "m.room.name", "")
			must.MatchGJSON(t, content, match.JSONKeyEqual("name", "Test Room"))
		})
		// sytest: POST /createRoom creates a room with the given version
		t.Run("POST /createRoom creates a room with the given version", func(t *testing.T) {
			t.Parallel()
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"room_version": "2",
				"preset":       "public_chat",
			})
			content := alice.MustGetStateEventContent(t, roomID, "m.room.create", "")
			must.MatchGJSON(t, content, match.JSONKeyEqual("room_version", "2"))
		})
		// sytest: POST /createRoom makes a private room with invites
		t.Run("POST /createRoom makes a private room with invites", func(t *testing.T) {
			t.Parallel()

			res := alice.CreateRoom(t, map[string]interface{}{
				"visibility": "private",
				"invite":     []string{bob.UserID},
			})
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("room_id", gjson.String),
				},
			})
		})
		// sytest: POST /createRoom rejects attempts to create rooms with numeric versions
		t.Run("POST /createRoom rejects attempts to create rooms with numeric versions", func(t *testing.T) {
			t.Parallel()

			res := alice.CreateRoom(t, map[string]interface{}{
				"visibility":   "private",
				"room_version": 1,
				"preset":       "public_chat",
			})
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 400,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_BAD_JSON"),
				},
			})
		})
		// sytest: POST /createRoom rejects attempts to create rooms with unknown versions
		t.Run("POST /createRoom rejects attempts to create rooms with unknown versions", func(t *testing.T) {
			t.Parallel()

			res := alice.CreateRoom(t, map[string]interface{}{
				"visibility":   "private",
				"room_version": "ahfgwjyerhgiuveisbruvybseyrugvi",
				"preset":       "public_chat",
			})
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 400,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_UNSUPPORTED_ROOM_VERSION"),
				},
			})
		})
		// sytest: Rooms can be created with an initial invite list (SYN-205)
		t.Run("Rooms can be created with an initial invite list (SYN-205)", func(t *testing.T) {
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"invite": []string{bob.UserID},
			})

			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
		})

		// sytest: Can /sync newly created room
		t.Run("Can /sync newly created room", func(t *testing.T) {
			roomID := alice.MustCreateRoom(t, map[string]interface{}{})

			// This will do the syncing for us
			alice.SendEventSynced(t, roomID, b.Event{
				Type:    "m.room.test",
				Content: map[string]interface{}{},
			})
		})

		// sytest: POST /createRoom ignores attempts to set the room version via creation_content
		t.Run("POST /createRoom ignores attempts to set the room version via creation_content", func(t *testing.T) {
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"creation_content": map[string]interface{}{
					"test":         "azerty",
					"room_version": "test",
				},
			})

			// Wait until user has joined the room
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

			// Get ordered timeline via a full sync
			res, _ := alice.MustSync(t, client.SyncReq{})

			roomObj := res.Get("rooms.join." + client.GjsonEscape(roomID))

			if !roomObj.Exists() {
				t.Fatalf("Room did not appear in sync")
			}

			event0 := roomObj.Get("timeline.events.0")

			if !event0.Exists() {
				t.Fatalf("First timeline event does not exist")
			}

			if event0.Get("type").Str != "m.room.create" {
				t.Fatalf("First event was not m.room.create: %s", event0)
			}
			if !event0.Get("content.room_version").Exists() {
				t.Fatalf("Room creation event did not have room version: %s", event0)
			}
			if event0.Get("content.room_version").Str == "test" {
				t.Fatalf("Room creation event room_version was a bogus room version: %s", event0)
			}
			if event0.Get("content.test").Str != "azerty" {
				t.Fatalf("Room creation event content 'test' key did not have expected value of 'azerty': %s", event0)
			}
		})
	})
}
