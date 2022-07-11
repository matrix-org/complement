//go:build msc3030
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
	"github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

func TestJumpToDateEndpoint(t *testing.T) {
	deployment := Deploy(t, b.BlueprintHSWithApplicationService)
	defer deployment.Destroy(t)

	// Create the normal user which will send messages in the room
	userID := "@alice:hs1"
	alice := deployment.Client(t, "hs1", userID)

	// Create the federated user which will fetch the messages from a remote homeserver
	remoteUserID := "@charlie:hs2"
	remoteCharlie := deployment.Client(t, "hs2", remoteUserID)

	// Create the application service bridge user that can use the ?ts query parameter
	asUserID := "@the-bridge-user:hs1"
	as := deployment.Client(t, "hs1", asUserID)

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

		// Just a sanity check that we're not leaking anything from the `/timestamp_to_event` endpoint
		t.Run("should not be able to query a private room you are not a member of", func(t *testing.T) {
			t.Parallel()
			timeBeforeRoomCreation := time.Now()

			// Alice will create the private room
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})

			// We will use Bob to query the room they're not a member of
			nonMemberUser := deployment.Client(t, "hs1", "@bob:hs1")

			// Make the `/timestamp_to_event` request from Bob's perspective (non room member)
			timestamp := makeTimestampFromTime(timeBeforeRoomCreation)
			timestampString := strconv.FormatInt(timestamp, 10)
			timestampToEventRes := nonMemberUser.DoFunc(t, "GET", []string{"_matrix", "client", "unstable", "org.matrix.msc3030", "rooms", roomID, "timestamp_to_event"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
				"ts":  []string{timestampString},
				"dir": []string{"f"},
			}))

			// A random user is not allowed to query for events in a private room
			// they're not a member of (forbidden).
			if timestampToEventRes.StatusCode != 403 {
				t.Fatalf("/timestamp_to_event returned %d HTTP status code but expected %d", timestampToEventRes.StatusCode, 403)
			}
		})

		// Just a sanity check that we're not leaking anything from the `/timestamp_to_event` endpoint
		t.Run("should not be able to query a public room you are not a member of", func(t *testing.T) {
			t.Parallel()
			timeBeforeRoomCreation := time.Now()

			// Alice will create the public room
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "public_chat",
			})

			// We will use Bob to query the room they're not a member of
			nonMemberUser := deployment.Client(t, "hs1", "@bob:hs1")

			// Make the `/timestamp_to_event` request from Bob's perspective (non room member)
			timestamp := makeTimestampFromTime(timeBeforeRoomCreation)
			timestampString := strconv.FormatInt(timestamp, 10)
			timestampToEventRes := nonMemberUser.DoFunc(t, "GET", []string{"_matrix", "client", "unstable", "org.matrix.msc3030", "rooms", roomID, "timestamp_to_event"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
				"ts":  []string{timestampString},
				"dir": []string{"f"},
			}))

			// A random user is not allowed to query for events in a public room
			// they're not a member of (forbidden).
			if timestampToEventRes.StatusCode != 403 {
				t.Fatalf("/timestamp_to_event returned %d HTTP status code but expected %d", timestampToEventRes.StatusCode, 403)
			}
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

			t.Run("when looking backwards before the room was created, should be able to find event that was imported", func(t *testing.T) {
				t.Parallel()
				timeBeforeRoomCreation := time.Now()
				roomID, _, _ := createTestRoom(t, alice)

				// Join from the application service bridge user
				as.JoinRoom(t, roomID, []string{"hs1"})

				// Import a message in the room before the room was created. We have to
				// use an application service user because they are the only one
				// allowed to use the `?ts` query parameter.
				importTime := time.Date(2022, 01, 03, 0, 0, 0, 0, time.Local)
				importTimestamp := makeTimestampFromTime(importTime)
				timestampString := strconv.FormatInt(importTimestamp, 10)
				// We can't use as.SendEventSynced(...) because application services can't use the /sync API
				sendRes := as.DoFunc(t, "PUT", []string{"_matrix", "client", "r0", "rooms", roomID, "send", "m.room.message", getTxnID("findEventBeforeCreation-txn")}, client.WithContentType("application/json"), client.WithJSONBody(t, map[string]interface{}{
					"body":    "old imported event",
					"msgtype": "m.text",
				}), client.WithQueries(url.Values{
					"ts": []string{timestampString},
				}))
				sendBody := client.ParseJSON(t, sendRes)
				importedEventID := client.GetJSONFieldStr(t, sendBody, "event_id")
				// Make sure the imported event has reached the homeserver
				alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
					return ev.Get("event_id").Str == importedEventID
				}))

				remoteCharlie.JoinRoom(t, roomID, []string{"hs1"})
				mustCheckEventisReturnedForTime(t, remoteCharlie, roomID, timeBeforeRoomCreation, "b", importedEventID)
			})

			t.Run("can get pagination token after getting remote event from timestamp to event endpoint", func(t *testing.T) {
				t.Parallel()
				roomID, _, eventB := createTestRoom(t, alice)
				remoteCharlie.JoinRoom(t, roomID, []string{"hs1"})
				mustCheckEventisReturnedForTime(t, remoteCharlie, roomID, eventB.AfterTimestamp, "b", eventB.EventID)

				contextRes := remoteCharlie.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "context", eventB.EventID}, client.WithContentType("application/json"), client.WithQueries(url.Values{
					"limit": []string{"0"},
				}))
				contextResResBody := client.ParseJSON(t, contextRes)
				paginationToken := client.GetJSONFieldStr(t, contextResResBody, "end")
				logrus.WithFields(logrus.Fields{
					"paginationToken": paginationToken,
				}).Error("asdf")
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

	roomID = c.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

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
			"body":    "Message B",
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
			"Want %s given %s but got %s\n%s",
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

	// We need some padding for some lines to make them all align with the label.
	// Pad this out so it equals whatever the longest label is.
	paddingString := "       "

	resultantString := fmt.Sprintf("%s-- oldest events --\n", paddingString)

	givenTimestampAlreadyInserted := false
	givenTimestampMarker := decorateStringWithAnsiColor(fmt.Sprintf("%s-- givenTimestamp=%s --\n", paddingString, strconv.FormatInt(givenTimestamp, 10)), AnsiColorYellow)

	// We're iterating over the events from oldest-in-time -> newest-in-time
	for _, ev := range events {
		// As we go, keep checking whether the givenTimestamp is
		// older(before-in-time) than the current event and insert a timestamp
		// marker as soon as we find the spot
		if givenTimestamp < ev.Get("origin_server_ts").Int() && !givenTimestampAlreadyInserted {
			resultantString += givenTimestampMarker
			givenTimestampAlreadyInserted = true
		}

		eventID := ev.Get("event_id").String()
		eventIDString := eventID
		labelString := paddingString
		if eventID == expectedEventId {
			eventIDString = decorateStringWithAnsiColor(eventID, AnsiColorGreen)
			labelString = "(want) "
		} else if eventID == actualEventId {
			eventIDString = decorateStringWithAnsiColor(eventID, AnsiColorRed)
			labelString = " (got) "
		}

		resultantString += fmt.Sprintf(
			"%s%s (%s) - %s\n",
			labelString,
			eventIDString,
			strconv.FormatInt(ev.Get("origin_server_ts").Int(), 10),
			ev.Get("type").String(),
		)
	}

	// The givenTimestamp could be newer(after-in-time) than any of the other events
	if givenTimestamp > events[len(events)-1].Get("origin_server_ts").Int() && !givenTimestampAlreadyInserted {
		resultantString += givenTimestampMarker
		givenTimestampAlreadyInserted = true
	}

	resultantString += fmt.Sprintf("%s-- newest events --\n", paddingString)

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
