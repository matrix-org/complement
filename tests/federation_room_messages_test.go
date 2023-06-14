package tests

import (
	"net/url"
	"testing"
	"time"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/sirupsen/logrus"
)

func TestMessagesOverFederation(t *testing.T) {
	deployment := Deploy(t, b.BlueprintPerfManyMessages)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	remoteCharlie := deployment.Client(t, "hs2", "@charlie:hs2")

	t.Run("parallel", func(t *testing.T) {
		t.Run("asdf", func(t *testing.T) {
			t.Parallel()

			syncResult, _ := alice.MustSync(t, client.SyncReq{})
			joinedRooms := syncResult.Get("rooms.join|@keys")
			roomWithManyMessages := joinedRooms.Get("0").String()

			// logrus.WithFields(logrus.Fields{
			// 	"joinedRooms":          joinedRooms,
			// 	"roomWithManyMessages": roomWithManyMessages,
			// }).Error("asdf")

			remoteCharlie.JoinRoom(t, roomWithManyMessages, []string{"hs1"})

			messagesRes := remoteCharlie.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomWithManyMessages, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
				"dir":   []string{"b"},
				"limit": []string{"100"},
			}))
			messagesResBody := client.ParseJSON(t, messagesRes)
			eventIDs := client.GetJSONFieldStringArray(t, messagesResBody, "chunk")
			// end := client.GetJSONFieldStringArray(t, messagesResBody, "end")

			logrus.WithFields(logrus.Fields{
				"joinedRooms":          joinedRooms,
				"roomWithManyMessages": roomWithManyMessages,
				"eventIDsLength":       len(eventIDs),
				"eventIDs":             eventIDs,
			}).Error("asdf")

			// messagesRes2 := remoteCharlie.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomWithManyMessages, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
			// 	"dir":   []string{"b"},
			// 	"from":  end,
			// 	"limit": []string{"500"},
			// }))
			// messagesResBody2 := client.ParseJSON(t, messagesRes2)
			// eventIDs2 := client.GetJSONFieldStringArray(t, messagesResBody2, "chunk")

			// logrus.WithFields(logrus.Fields{
			// 	"joinedRooms":          joinedRooms,
			// 	"roomWithManyMessages": roomWithManyMessages,
			// 	"eventIDsLength":       len(eventIDs2),
			// 	"eventIDs":             eventIDs2,
			// }).Error("asdf2")

			time.Sleep(5 * time.Second)
		})
	})
}
