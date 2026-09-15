package tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

// The room version that enables MSC3389 ("Redaction changes for events with a relation").
const msc3389RoomVersion = "org.matrix.msc3389.10"

// sendRelatedEvent sends a reaction (m.annotation) targeting parentID, with an extra
// `key` field that should not survive redaction, and returns the event ID of the reaction.
func sendRelatedEvent(t *testing.T, alice *client.CSAPI, roomID, parentID string) string {
	return alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.reaction",
		Content: map[string]interface{}{
			"m.relates_to": map[string]interface{}{
				"rel_type": "m.annotation",
				"event_id": parentID,
				"key":      "👍",
			},
		},
		Sender: alice.UserID,
	})
}

// TestMSC3389RedactionPreservesRelation checks that, in a room version that implements
// MSC3389, redacting an event with an `m.relates_to` field keeps `rel_type` and `event_id`
// but strips every other key (such as the reaction `key`).
func TestMSC3389RedactionPreservesRelation(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	// Alice creates a room in the MSC3389 unstable room version.
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset":       "public_chat",
		"room_version": msc3389RoomVersion,
	})

	parentID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "root",
		},
		Sender: alice.UserID,
	})

	reactionID := sendRelatedEvent(t, alice, roomID, parentID)

	// Verify the original event is fully intact before redaction.
	eventJsonBefore := alice.MustGetEvent(t, roomID, reactionID)
	must.MatchGJSON(t, eventJsonBefore,
		match.JSONKeyEqual("content.m.relates_to.rel_type", "m.annotation"),
		match.JSONKeyEqual("content.m.relates_to.event_id", parentID),
		match.JSONKeyEqual("content.m.relates_to.key", "👍"),
	)

	alice.MustSendRedaction(t, roomID, map[string]interface{}{}, reactionID)

	// After redaction the `m.relates_to` object is retained, but only `rel_type` and
	// `event_id` survive; the `key` is removed.
	eventJsonAfter := alice.MustGetEvent(t, roomID, reactionID)
	must.MatchGJSON(t, eventJsonAfter,
		match.JSONKeyEqual("content.m.relates_to.rel_type", "m.annotation"),
		match.JSONKeyEqual("content.m.relates_to.event_id", parentID),
		match.JSONKeyMissing("content.m.relates_to.key"),
	)
}

// TestMSC3389RedactionStripsRelationInOlderVersions checks the contrast: in a room version
// that predates MSC3389 (e.g. v9), redaction removes the whole `m.relates_to` field.
func TestMSC3389RedactionStripsRelationInOlderVersions(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset":       "public_chat",
		"room_version": "9",
	})

	parentID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "root",
		},
		Sender: alice.UserID,
	})

	reactionID := sendRelatedEvent(t, alice, roomID, parentID)

	alice.MustSendRedaction(t, roomID, map[string]interface{}{}, reactionID)

	// In room v9 the whole `m.relates_to` is stripped, leaving no content.
	eventJsonAfter := alice.MustGetEvent(t, roomID, reactionID)
	must.MatchGJSON(t, eventJsonAfter,
		match.JSONKeyMissing("content.m.relates_to"),
	)
}

// TestMSC3389RedactionPreservesPlainRelation checks that an `m.relates_to` containing only
// `rel_type` and `event_id` (with no extra keys) survives redaction unchanged.
func TestMSC3389RedactionPreservesPlainRelation(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset":       "public_chat",
		"room_version": msc3389RoomVersion,
	})

	parentID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "root",
		},
		Sender: alice.UserID,
	})

	// A threaded reply has no extra `m.relates_to` keys beyond rel_type + event_id.
	replyID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "reply",
			"m.relates_to": map[string]interface{}{
				"rel_type": "m.thread",
				"event_id": parentID,
			},
		},
		Sender: alice.UserID,
	})

	alice.MustSendRedaction(t, roomID, map[string]interface{}{}, replyID)

	eventJsonAfter := alice.MustGetEvent(t, roomID, replyID)
	must.MatchGJSON(t, eventJsonAfter,
		match.JSONKeyEqual("content.m.relates_to.rel_type", "m.thread"),
		match.JSONKeyEqual("content.m.relates_to.event_id", parentID),
	)
}
