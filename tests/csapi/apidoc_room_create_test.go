package csapi_tests

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func doCreateRoom(t *testing.T, c *client.CSAPI, json map[string]interface{}, match match.HTTPResponse) {
	res := c.Do(t, "POST", []string{"_matrix", "client", "v3", "createRoom"}, client.WithJSONBody(t, json))
	must.MatchResponse(t, res, match)
}

func TestRoomCreate(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	t.Run("Parallel", func(t *testing.T) {
		// sytest: POST /createRoom makes a public room
		t.Run("POST /createRoom makes a public room", func(t *testing.T) {
			t.Parallel()
			roomAlias := "30-room-create-alias-random"

			doCreateRoom(t, alice, map[string]interface{}{
				"visibility":      "public",
				"room_alias_name": roomAlias,
			}, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("room_id", gjson.String),
				},
			})
		})
		// sytest: POST /createRoom makes a private room
		t.Run("POST /createRoom makes a private room", func(t *testing.T) {
			t.Parallel()

			doCreateRoom(t, alice, map[string]interface{}{
				"visibility": "private",
			}, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("room_id", gjson.String),
				},
			})
		})
		// sytest: POST /createRoom makes a room with a topic
		t.Run("POST /createRoom makes a room with a topic", func(t *testing.T) {
			t.Parallel()

			roomID := alice.CreateRoom(t, map[string]interface{}{
				"topic":  "Test Room",
				"preset": "public_chat",
			})
			res := alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.topic"})
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyEqual("topic", "Test Room"),
				},
			})
		})
		// sytest: POST /createRoom makes a room with a name
		t.Run("POST /createRoom makes a room with a name", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"name":   "Test Room",
				"preset": "public_chat",
			})
			res := alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.name"})
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyEqual("name", "Test Room"),
				},
			})
		})
		// sytest: POST /createRoom creates a room with the given version
		t.Run("POST /createRoom creates a room with the given version", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"room_version": "2",
				"preset":       "public_chat",
			})
			res := alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.create"})
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyEqual("room_version", "2"),
				},
			})
		})
		// sytest: POST /createRoom makes a private room with invites
		t.Run("POST /createRoom makes a private room with invites", func(t *testing.T) {
			t.Parallel()

			doCreateRoom(t, alice, map[string]interface{}{
				"visibility": "private",
				"invite":     []string{bob.UserID},
			}, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyTypeEqual("room_id", gjson.String),
				},
			})
		})
		// sytest: POST /createRoom rejects attempts to create rooms with numeric versions
		t.Run("POST /createRoom rejects attempts to create rooms with numeric versions", func(t *testing.T) {
			t.Parallel()

			doCreateRoom(t, alice, map[string]interface{}{
				"visibility":   "private",
				"room_version": 1,
				"preset":       "public_chat",
			}, match.HTTPResponse{
				StatusCode: 400,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_BAD_JSON"),
				},
			})
		})
		// sytest: POST /createRoom rejects attempts to create rooms with unknown versions
		t.Run("POST /createRoom rejects attempts to create rooms with unknown versions", func(t *testing.T) {
			t.Parallel()

			doCreateRoom(t, alice, map[string]interface{}{
				"visibility":   "private",
				"room_version": "ahfgwjyerhgiuveisbruvybseyrugvi",
				"preset":       "public_chat",
			}, match.HTTPResponse{
				StatusCode: 400,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_UNSUPPORTED_ROOM_VERSION"),
				},
			})
		})
		// sytest: Rooms can be created with an initial invite list (SYN-205)
		t.Run("Rooms can be created with an initial invite list (SYN-205)", func(t *testing.T) {
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"invite": []string{bob.UserID},
			})

			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
		})

		// sytest: Can /sync newly created room
		t.Run("Can /sync newly created room", func(t *testing.T) {
			roomID := alice.CreateRoom(t, map[string]interface{}{})

			// This will do the syncing for us
			alice.SendEventSynced(t, roomID, b.Event{
				Type:    "m.room.test",
				Content: map[string]interface{}{},
			})
		})

		// sytest: POST /createRoom ignores attempts to set the room version via creation_content
		t.Run("POST /createRoom ignores attempts to set the room version via creation_content", func(t *testing.T) {
			roomID := alice.CreateRoom(t, map[string]interface{}{
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
