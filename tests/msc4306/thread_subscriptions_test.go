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

func TestThreadSubscriptions(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	t.Run("Can subscribe to and unsubscribe from a thread", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{})
		threadRootID := alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "what do you think? reply in a thread!",
			},
		})

		// Subscribe to the thread manually
		alice.MustDo(t, "PUT", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"}, client.WithJSONBody(t, map[string]interface{}{}))

		must.MatchResponse(t, alice.MustDo(t, "GET", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"}), match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("automatic", false),
			},
		})

		// Unsubscribe from the thread
		alice.MustDo(t, "DELETE", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"})

		must.MatchResponse(t, alice.Do(t, "GET", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"}), match.HTTPResponse{
			StatusCode: 404,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_NOT_FOUND"),
			},
		})
	})

	t.Run("Cannot use thread root as automatic subscription cause event", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{})
		threadRootID := alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "what do you think? reply in a thread!",
			},
		})

		response := alice.Do(t, "PUT", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"}, client.WithJSONBody(t, map[string]interface{}{
			"automatic": threadRootID,
		}))

		must.MatchResponse(t, response, match.HTTPResponse{
			StatusCode: 400,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "IO.ELEMENT.MSC4306.M_NOT_IN_THREAD"),
			},
		})
	})

	t.Run("Can create automatic subscription to a thread", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{})
		threadRootID := alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "what do you think? reply in a thread!",
			},
		})

		// Create a message in the thread
		threadReplyID := alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "this is a reply",
				"m.relates_to": map[string]interface{}{
					"rel_type": "m.thread",
					"event_id": threadRootID,
				},
			},
		})

		alice.MustDo(t, "PUT", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"}, client.WithJSONBody(t, map[string]interface{}{
			"automatic": threadReplyID,
		}))

		must.MatchResponse(t, alice.MustDo(t, "GET", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"}), match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("automatic", true),
			},
		})
	})

	t.Run("Manual subscriptions overwrite automatic subscriptions", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{})
		threadRootID := alice.SendEventSynced(t, roomID, b.Event{
			Type:    "m.room.message",
			Content: map[string]interface{}{"body": "Thread Root", "msgtype": "m.text"},
		})

		// Create a message in the thread
		threadReplyID := alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"body":    "Thread Reply",
				"msgtype": "m.text",
				"m.relates_to": map[string]interface{}{
					"rel_type": "m.thread",
					"event_id": threadRootID,
				},
			},
		})

		// Create automatic subscription first
		alice.MustDo(t, "PUT", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"}, client.WithJSONBody(t, map[string]interface{}{
			"automatic": threadReplyID,
		}))

		// Then create manual subscription
		alice.MustDo(t, "PUT", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"}, client.WithJSONBody(t, map[string]interface{}{}))

		must.MatchResponse(t, alice.MustDo(t, "GET", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"}), match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("automatic", false),
			},
		})
	})

	t.Run("Error when using invalid automatic event ID", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{})
		threadRootID := alice.SendEventSynced(t, roomID, b.Event{
			Type:    "m.room.message",
			Content: map[string]interface{}{"body": "Thread Root", "msgtype": "m.text"},
		})
		otherThreadRootID := alice.SendEventSynced(t, roomID, b.Event{
			Type:    "m.room.message",
			Content: map[string]interface{}{"body": "another thread root", "msgtype": "m.text"},
		})

		// Send message, but *not* in the right thread
		otherThreadReplyID := alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"body":    "Not in the same thread",
				"msgtype": "m.text",
				"m.relates_to": map[string]interface{}{
					"rel_type": "m.thread",
					"event_id": otherThreadRootID,
				},
			},
		})

		// We can't create an automatic subscription to the first thread with
		// the message in the wrong thread
		response := alice.Do(t, "PUT", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"}, client.WithJSONBody(t, map[string]interface{}{
			"automatic": otherThreadReplyID,
		}))

		must.MatchResponse(t, response, match.HTTPResponse{
			StatusCode: 400,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "IO.ELEMENT.MSC4306.M_NOT_IN_THREAD"),
			},
		})
	})

	// Tests idempotence
	t.Run("Unsubscribe succeeds even with no subscription", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{})
		threadRootID := alice.SendEventSynced(t, roomID, b.Event{
			Type:    "m.room.message",
			Content: map[string]interface{}{"body": "Thread Root", "msgtype": "m.text"},
		})

		// Unsubscribe, but without being subscribed first
		response := alice.Do(t, "DELETE", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"})

		must.MatchResponse(t, response, match.HTTPResponse{
			StatusCode: 200,
		})
	})

	t.Run("Nonexistent threads return 404", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{})
		nonExistentID := "$notathread:example.org"

		// try PUT
		response := alice.Do(t, "PUT", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", nonExistentID, "subscription"}, client.WithJSONBody(t, map[string]interface{}{}))
		must.MatchResponse(t, response, match.HTTPResponse{
			StatusCode: 404,
		})

		// try GET
		response = alice.Do(t, "GET", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", nonExistentID, "subscription"})
		must.MatchResponse(t, response, match.HTTPResponse{
			StatusCode: 404,
		})
	})

	t.Run("Server-side automatic subscription ordering conflict", func(t *testing.T) {
		/*
			The desired timeline of events and subscriptions is as such:

			1. threadRoot
			2. threadReply1
			3. auto-subscribe
			4. threadReply2
			5. unsubscribe
			4. threadReply3
			6. try to auto-subscribe using threadReply1: denied
			7. try to auto-subscribe using threadReply2: denied
			8. try to auto-subscribe using threadReply3: OK
		*/

		roomID := alice.MustCreateRoom(t, map[string]interface{}{})

		// 1. Create a thread root message
		threadRootID := alice.SendEventSynced(t, roomID, b.Event{
			Type:    "m.room.message",
			Content: map[string]interface{}{"body": "Thread Root", "msgtype": "m.text"},
		})

		// 2.
		threadReply1ID := alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"body":    "Thread Reply 1",
				"msgtype": "m.text",
				"m.relates_to": map[string]interface{}{
					"rel_type": "m.thread",
					"event_id": threadRootID,
				},
			},
		})

		// 3.
		alice.MustDo(t, "PUT", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"}, client.WithJSONBody(t, map[string]interface{}{
			"automatic": threadReply1ID,
		}))

		// 4.
		threadReply2ID := alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"body":    "Thread Reply 2",
				"msgtype": "m.text",
				"m.relates_to": map[string]interface{}{
					"rel_type": "m.thread",
					"event_id": threadRootID,
				},
			},
		})

		// 5.
		alice.MustDo(t, "DELETE", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"})

		// 6.
		threadReply3ID := alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"body":    "Thread Reply 3",
				"msgtype": "m.text",
				"m.relates_to": map[string]interface{}{
					"rel_type": "m.thread",
					"event_id": threadRootID,
				},
			},
		})

		// 7.
		response := alice.Do(t, "PUT", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"}, client.WithJSONBody(t, map[string]interface{}{
			"automatic": threadReply1ID,
		}))

		must.MatchResponse(t, response, match.HTTPResponse{
			StatusCode: 409,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "IO.ELEMENT.MSC4306.M_CONFLICTING_UNSUBSCRIPTION"),
			},
		})

		response = alice.Do(t, "GET", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"})
		must.MatchResponse(t, response, match.HTTPResponse{
			StatusCode: 404,
		})

		// 8.
		response = alice.Do(t, "PUT", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"}, client.WithJSONBody(t, map[string]interface{}{
			"automatic": threadReply2ID,
		}))

		must.MatchResponse(t, response, match.HTTPResponse{
			StatusCode: 409,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "IO.ELEMENT.MSC4306.M_CONFLICTING_UNSUBSCRIPTION"),
			},
		})

		response = alice.Do(t, "GET", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"})
		must.MatchResponse(t, response, match.HTTPResponse{
			StatusCode: 404,
		})

		// 9.
		response = alice.Do(t, "PUT", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"}, client.WithJSONBody(t, map[string]interface{}{
			"automatic": threadReply3ID,
		}))

		must.MatchResponse(t, response, match.HTTPResponse{
			StatusCode: 200,
		})

		response = alice.Do(t, "GET", []string{"_matrix", "client", "unstable", "io.element.msc4306", "rooms", roomID, "thread", threadRootID, "subscription"})
		must.MatchResponse(t, response, match.HTTPResponse{
			StatusCode: 200,
		})
	})
}
