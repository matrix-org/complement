package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

// Test `PUT /_matrix/client/v3/rooms/{roomId}/redact/{eventId}/{txnId} ` (redactions)
func TestRedact(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
	})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "bob",
	})

	// Alice creates the room
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		// Just making it easier for everyone to join
		"preset": "public_chat",
	})
	// Bob joins the room
	bob.MustJoinRoom(t, roomID, nil)

	t.Run("Event content is redacted", func(t *testing.T) {
		// Alice creates an event
		expectedEventContent := map[string]interface{}{
				"msgtype": "m.text",
				"body":    "expected message body",
			}
		eventIDToRedact := alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content:expectedEventContent,
		})

		// Bob can see the event content
		eventJsonBefore := bob.MustGetEvent(t, roomID, eventIDToRedact)
		must.MatchGJSON(t, eventJsonBefore, match.JSONKeyEqual("content", expectedEventContent))

		// Alice redacts the event
		alice.MustSendRedaction(t, roomID, map[string]interface{}{
			"reason": "reasons...",
		}, eventIDToRedact)

		// Bob can no longer see the event content
		eventJsonAfter := bob.MustGetEvent(t, roomID, eventIDToRedact)
		must.MatchGJSON(t, eventJsonAfter, match.JSONKeyEqual("content", map[string]interface{}{
			// no content
		}))
	})

}
