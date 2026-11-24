package csapi_tests

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
)

// TestRoomContext checks the response shape of
// GET /_matrix/client/v3/rooms/{roomId}/context/{eventId}, where all of `start`, `end`,
// `events_before`, `event`, `events_after` and `state` are required.
func TestRoomContext(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})

	// Chronological order. Nothing else is sent into the room after these.
	messageIDs := make([]string, 5)
	for i := range messageIDs {
		messageIDs[i] = alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    fmt.Sprintf("TestRoomContext message %d", i),
			},
		})
	}

	stateRes := alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state"})
	var createEventID string
	gjson.ParseBytes(client.ParseJSON(t, stateRes)).ForEach(func(_, ev gjson.Result) bool {
		if ev.Get("type").Str == "m.room.create" && ev.Get("state_key").Str == "" {
			createEventID = ev.Get("event_id").Str
			return false
		}
		return true
	})
	if createEventID == "" {
		t.Fatalf("no m.room.create event in the room state")
	}

	getContext := func(t *testing.T, eventID string, query url.Values) *http.Response {
		t.Helper()
		return alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "context", eventID}, client.WithQueries(query))
	}

	allFieldsPresent := func(eventID string) []match.JSON {
		return []match.JSON{
			jsonKeyNonEmptyString("start"),
			jsonKeyNonEmptyString("end"),
			match.JSONArrayEach("events_before", func(gjson.Result) error { return nil }),
			match.JSONKeyEqual("event.event_id", eventID),
			match.JSONArrayEach("events_after", func(gjson.Result) error { return nil }),
			match.JSONArrayEach("state", func(gjson.Result) error { return nil }),
		}
	}

	t.Run("Parallel", func(t *testing.T) {
		t.Run("limit=0 returns all fields with empty event arrays", func(t *testing.T) {
			runtime.SkipIf(t, runtime.Dendrite) // omits `state` when `limit` is 0
			t.Parallel()
			res := getContext(t, messageIDs[2], url.Values{"limit": []string{"0"}})
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: append(allFieldsPresent(messageIDs[2]),
					match.JSONKeyArrayOfSize("events_before", 0),
					match.JSONKeyArrayOfSize("events_after", 0),
				),
			})
		})

		t.Run("Default limit returns events in the right order", func(t *testing.T) {
			t.Parallel()
			res := getContext(t, messageIDs[2], nil)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: append(allFieldsPresent(messageIDs[2]),
					// events_before is reverse-chronological and also includes the
					// room's initial state events after these two.
					match.JSONKeyEqual("events_before.0.event_id", messageIDs[1]),
					match.JSONKeyEqual("events_before.1.event_id", messageIDs[0]),
					match.JSONKeyArrayOfSize("events_after", 2),
					match.JSONKeyEqual("events_after.0.event_id", messageIDs[3]),
					match.JSONKeyEqual("events_after.1.event_id", messageIDs[4]),
				),
			})
		})

		t.Run("Context of the create event has a start token that paginates to nothing", func(t *testing.T) {
			runtime.SkipIf(t, runtime.Dendrite) // returns an empty `start` when there are no earlier events
			t.Parallel()
			res := getContext(t, createEventID, nil)
			body := must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: append(allFieldsPresent(createEventID),
					match.JSONKeyArrayOfSize("events_before", 0),
				),
			})

			messagesRes := alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(url.Values{
				"dir":  []string{"b"},
				"from": []string{gjson.GetBytes(body, "start").Str},
			}))
			must.MatchResponse(t, messagesRes, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONKeyArrayOfSize("chunk", 0),
					match.JSONKeyMissing("end"),
				},
			})
		})

		t.Run("Context of the latest event has an end token that paginates to nothing", func(t *testing.T) {
			runtime.SkipIf(t, runtime.Dendrite) // returns an empty `end` when there are no later events
			t.Parallel()
			latestEventID := messageIDs[len(messageIDs)-1]
			res := getContext(t, latestEventID, nil)
			body := must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: append(allFieldsPresent(latestEventID),
					match.JSONKeyArrayOfSize("events_after", 0),
				),
			})

			messagesRes := alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(url.Values{
				"dir":  []string{"f"},
				"from": []string{gjson.GetBytes(body, "end").Str},
			}))
			must.MatchResponse(t, messagesRes, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONKeyArrayOfSize("chunk", 0),
				},
			})
		})

		t.Run("Unknown event returns 404", func(t *testing.T) {
			t.Parallel()
			res := getContext(t, "$unknown-event-id", nil)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusNotFound,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_NOT_FOUND"),
				},
			})
		})
	})
}

func jsonKeyNonEmptyString(wantKey string) match.JSON {
	return func(body gjson.Result) error {
		res := body.Get(wantKey)
		if !res.Exists() {
			return fmt.Errorf("key '%s' missing", wantKey)
		}
		if res.Type != gjson.String || res.Str == "" {
			return fmt.Errorf("key '%s' is not a non-empty string: %s", wantKey, res.Raw)
		}
		return nil
	}
}
