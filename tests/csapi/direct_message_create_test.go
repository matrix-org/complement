package csapi_tests

import (
	"net/url"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/tidwall/gjson"
)

func TestDirectMessageCreate(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	// Alice invites Bob to a room for direct messaging.
	roomID := alice.CreateRoom(t, map[string]interface{}{
		"invite":    []string{bob.UserID},
		"is_direct": true,
	})

	// Bob receives the invite and joins.
	next_batch := bob.SyncUntilInvitedTo(t, roomID)
	bob.JoinRoom(t, roomID, []string{"hs1"})

	// Wait until Bob sees that he's joined.
	isBobJoinEvent := func(ev gjson.Result) bool {
		return ev.Get("type").Str == "m.room.member" && ev.Get("state_key").Str == bob.UserID && ev.Get("content").Get("membership").Str == "join"
	}
	bob.SyncUntilTimelineHas(t, roomID, isBobJoinEvent)

	// Repeat the sync, but in a form where we can inspect the output.
	query := url.Values{
		"timeout": []string{"1000"},
		"since":   []string{next_batch},
	}

	res := bob.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "sync"}, client.WithQueries(query))
	body := client.ParseJSON(t, res)
	eventRes := gjson.GetBytes(body, "rooms.join").Get(roomID).Get("timeline.events")
	if !eventRes.IsArray() {
		t.Fatal("events in timeline was not an array: ", eventRes.Raw)
	}
	events := eventRes.Array()
	bobJoinEvents := 0
	for _, ev := range events {
		if isBobJoinEvent(ev) {
			bobJoinEvents += 1
		}
	}
	if bobJoinEvents != 1 {
		t.Fatalf("Saw %d join events for Bob; expected 1", bobJoinEvents)
	}
}
