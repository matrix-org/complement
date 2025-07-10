//go:build !dendrite_blacklist
// +build !dendrite_blacklist

package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

func TestRoomCreateRichTopic(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	t.Run("Parallel", func(t *testing.T) {
		// POST /createRoom sets m.topic when the topic parameter is supplied
		t.Run("POST /createRoom sets m.topic when the topic parameter is supplied", func(t *testing.T) {
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
		// POST /createRoom makes sets m.topic when the topic parameter is supplied in addition to initial_state
		t.Run("POST /createRoom makes sets m.topic when the topic parameter is supplied in addition to initial_state", func(t *testing.T) {
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
	})
}
