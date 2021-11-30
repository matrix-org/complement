// +build msc3030

// This file contains tests for a jump to date API endpoint,
// currently experimental feature defined by MSC3030, which you can read here:
// https://github.com/matrix-org/matrix-doc/pull/3030

package tests

import (
	"fmt"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/tidwall/gjson"
)

func TestJumpToDateEndpoint(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationTwoLocalOneRemote)
	defer deployment.Destroy(t)

	// Create the normal user which will send messages in the room
	userID := "@alice:hs1"
	alice := deployment.Client(t, "hs1", userID)

	// Create the federated user which will fetch the messages from a remote homeserver
	remoteUserID := "@charlie:hs2"
	remoteCharlie := deployment.Client(t, "hs2", remoteUserID)

	t.Run("parallel", func(t *testing.T) {
		t.Run("should find event after given timestmap", func(t *testing.T) {
			t.Parallel()
			roomID, eventA, _ := createTestRoom(t, alice)
			mustCheckEventisReturnedForTime(t, alice, roomID, eventA.BeforeTimestamp, "f", eventA.EventID)
		})

		t.Run("should find event before given timestmap", func(t *testing.T) {
			t.Parallel()
			roomID, _, eventB := createTestRoom(t, alice)
			mustCheckEventisReturnedForTime(t, alice, roomID, eventB.AfterTimestamp, "b", eventB.EventID)
		})

		t.Run("should find nothing before the earliest timestmap", func(t *testing.T) {
			t.Parallel()
			timeBeforeRoomCreation := time.Now()
			roomID, _, _ := createTestRoom(t, alice)
			mustCheckEventisReturnedForTime(t, alice, roomID, timeBeforeRoomCreation, "b", "")
		})

		t.Run("should find nothing after the latest timestmap", func(t *testing.T) {
			t.Parallel()
			roomID, _, eventB := createTestRoom(t, alice)
			mustCheckEventisReturnedForTime(t, alice, roomID, eventB.AfterTimestamp, "f", "")
		})

		t.Run("federation", func(t *testing.T) {
			t.Run("looking forwards, should be able to find event that was sent before we joined", func(t *testing.T) {
				t.Parallel()
				roomID, eventA, _ := createTestRoom(t, alice)
				remoteCharlie.JoinRoom(t, roomID, []string{"hs1"})
				mustCheckEventisReturnedForTime(t, remoteCharlie, roomID, eventA.BeforeTimestamp, "f", eventA.EventID)
			})

			t.Run("looking backwards, should be able to find event that was sent before we joined", func(t *testing.T) {
				t.Parallel()
				roomID, _, eventB := createTestRoom(t, alice)
				remoteCharlie.JoinRoom(t, roomID, []string{"hs1"})
				mustCheckEventisReturnedForTime(t, remoteCharlie, roomID, eventB.AfterTimestamp, "b", eventB.EventID)
			})
		})
	})
}

type eventTime struct {
	EventID         string
	BeforeTimestamp time.Time
	AfterTimestamp  time.Time
}

func createTestRoom(t *testing.T, c *client.CSAPI) (roomID string, eventA, eventB *eventTime) {
	t.Helper()

	roomID = c.CreateRoom(t, map[string]interface{}{})
	//c.JoinRoom(t, roomID, nil)

	timeBeforeEventA := time.Now()
	eventAID := c.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message A",
		},
	})
	timeAfterEventA := time.Now()

	eventBID := c.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message A",
		},
	})
	timeAfterEventB := time.Now()

	eventA = &eventTime{EventID: eventAID, BeforeTimestamp: timeBeforeEventA, AfterTimestamp: timeAfterEventA}
	eventB = &eventTime{EventID: eventBID, BeforeTimestamp: timeAfterEventA, AfterTimestamp: timeAfterEventB}

	return roomID, eventA, eventB
}

// Fetch event from /timestamp_to_event and ensure it matches the expectedEventId
func mustCheckEventisReturnedForTime(t *testing.T, c *client.CSAPI, roomID string, givenTime time.Time, direction string, expectedEventId string) {
	t.Helper()

	givenTimestamp := makeTimestampFromTime(givenTime)
	timestampString := strconv.FormatInt(givenTimestamp, 10)
	timestampToEventRes := c.DoFunc(t, "GET", []string{"_matrix", "client", "unstable", "org.matrix.msc3030", "rooms", roomID, "timestamp_to_event"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
		"ts":  []string{timestampString},
		"dir": []string{direction},
	}))
	timestampToEventResBody := client.ParseJSON(t, timestampToEventRes)

	// Only allow a 200 response meaning we found an event or a 404 meaning we didn't.
	// Other status codes will throw and assumed to be application errors.
	actualEventId := ""
	if timestampToEventRes.StatusCode == 200 {
		actualEventId = client.GetJSONFieldStr(t, timestampToEventResBody, "event_id")
	} else if timestampToEventRes.StatusCode != 404 {
		t.Fatalf("mustCheckEventisReturnedForTime: /timestamp_to_event request failed with status=%d", timestampToEventRes.StatusCode)
	}

	if actualEventId != expectedEventId {
		debugMessageList := getDebugMessageListFromMessagesResponse(t, c, roomID, expectedEventId, actualEventId, givenTimestamp)
		t.Fatalf(
			"Expected to see %s given %s but received %s\n%s",
			decorateStringWithAnsiColor(expectedEventId, AnsiColorGreen),
			decorateStringWithAnsiColor(timestampString, AnsiColorYellow),
			decorateStringWithAnsiColor(actualEventId, AnsiColorRed),
			debugMessageList,
		)
	}
}

func getDebugMessageListFromMessagesResponse(t *testing.T, c *client.CSAPI, roomID string, expectedEventId string, actualEventId string, givenTimestamp int64) string {
	t.Helper()

	messagesRes := c.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
		// The events returned will be from the newest -> oldest since we're going backwards
		"dir":   []string{"b"},
		"limit": []string{"100"},
	}))
	messsageResBody := client.ParseJSON(t, messagesRes)

	wantKey := "chunk"
	keyRes := gjson.GetBytes(messsageResBody, wantKey)
	if !keyRes.Exists() {
		t.Fatalf("missing key '%s'", wantKey)
	}
	if !keyRes.IsArray() {
		t.Fatalf("key '%s' is not an array (was %s)", wantKey, keyRes.Type)
	}

	// Make the events go from oldest-in-time -> newest-in-time
	events := reverseGjsonArray(keyRes.Array())
	if len(events) == 0 {
		t.Fatalf(
			"getDebugMessageListFromMessagesResponse found no messages in the room(%s).",
			roomID,
		)
	}
	resultantString := "(oldest)\n"
	givenTimestampAlreadyInserted := false
	givenTimestampMarker := decorateStringWithAnsiColor(fmt.Sprintf("-- givenTimestamp=%s --\n", strconv.FormatInt(givenTimestamp, 10)), AnsiColorYellow)

	// We're iterating over the events from oldest-in-time -> newest-in-time
	for _, ev := range events {
		// As we go, keep checking whether the givenTimestamp is
		// older(before-in-time) than the current event and insert a timestamp
		// marker as soon as we find the spot
		if givenTimestamp < ev.Get("origin_server_ts").Int() && !givenTimestampAlreadyInserted {
			resultantString += givenTimestampMarker
			givenTimestampAlreadyInserted = true
		}

		event_id := ev.Get("event_id").String()
		event_id_string := event_id
		if event_id == expectedEventId {
			event_id_string = decorateStringWithAnsiColor(event_id, AnsiColorGreen)
		} else if event_id == actualEventId {
			event_id_string = decorateStringWithAnsiColor(event_id, AnsiColorRed)
		}

		resultantString += fmt.Sprintf("%s (%s) - %s\n", event_id_string, strconv.FormatInt(ev.Get("origin_server_ts").Int(), 10), ev.Get("type").String())
	}

	// The givenTimestamp could be newer(after-in-time) than any of the other events
	if givenTimestamp > events[len(events)-1].Get("origin_server_ts").Int() && !givenTimestampAlreadyInserted {
		resultantString += givenTimestampMarker
		givenTimestampAlreadyInserted = true
	}

	resultantString += "(newest)\n"

	return resultantString
}

func makeTimestampFromTime(t time.Time) int64 {
	return t.UnixNano() / int64(time.Millisecond)
}

const AnsiColorRed string = "31"
const AnsiColorGreen string = "32"
const AnsiColorYellow string = "33"

func decorateStringWithAnsiColor(inputString, decorationColor string) string {
	return fmt.Sprintf("\033[%sm%s\033[0m", decorationColor, inputString)
}

func reverseGjsonArray(in []gjson.Result) []gjson.Result {
	out := make([]gjson.Result, len(in))
	for i := 0; i < len(in); i++ {
		out[i] = in[len(in)-i-1]
	}
	return out
}
