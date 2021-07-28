// +build msc3030

// This file contains tests for a jump to date API endpoint,
// currently experimental feature defined by MSC3030, which you can read here:
// https://github.com/matrix-org/matrix-doc/pull/3030

package tests

import (
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
	deployment := Deploy(t, b.BlueprintFederationTwoLocalOneRemote)
	defer deployment.Destroy(t)

	// Create the normal user which will send messages in the room
	userID := "@alice:hs1"
	alice := deployment.Client(t, "hs1", userID)

	roomID := alice.CreateRoom(t, map[string]interface{}{})
	alice.JoinRoom(t, roomID, nil)

	timeBeforeEventA := time.Now()
	eventAID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message A",
		},
	})
	eventBID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message A",
		},
	})
	timeAfterEventB := time.Now()

	logrus.WithFields(logrus.Fields{
		"eventAID": eventAID,
		"eventBID": eventBID,
	}).Error("see messages")

	t.Run("parallel", func(t *testing.T) {
		t.Run("should find event after given timestmap", func(t *testing.T) {
			checkEventisReturnedForTime(t, alice, roomID, timeBeforeEventA, eventAID)
		})

		t.Run("should find event before given timestmap", func(t *testing.T) {
			checkEventisReturnedForTime(t, alice, roomID, timeAfterEventB, eventBID)
		})

	})
}

func makeTimestampFromTime(t time.Time) int64 {
	return t.UnixNano() / int64(time.Millisecond)
}

func checkEventisReturnedForTime(t *testing.T, c *client.CSAPI, roomID string, givenTime time.Time, expectedEventId string) {
	t.Helper()

	timestampString := strconv.FormatInt(makeTimestampFromTime(givenTime), 10)
	timestampToEventRes := c.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "timestamp_to_event"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
		"ts": []string{timestampString},
	}))
	timestampToEventResBody := client.ParseJSON(t, timestampToEventRes)

	actualEventIdRes := gjson.GetBytes(timestampToEventResBody, "event_id")
	actualEventId := actualEventIdRes.Str

	if actualEventId != expectedEventId {
		actualEvent := c.GetEvent(t, roomID, actualEventId)
		t.Fatalf("Expected to see %s but received %s\n%+v", expectedEventId, actualEventId, actualEvent)
	}
}
