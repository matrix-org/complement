package csapi_tests

import (
	"io/ioutil"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
)

// This is technically a tad different from the sytest, in that it doesnt try to ban a @random_dude:test,
// but this will actually validate against a present user in the room.
// sytest: Non-present room members cannot ban others
func TestNotPresentUserCannotBanOthers(t *testing.T) {
	deployment := Deploy(t, b.MustValidate(b.Blueprint{
		Name: "abc",
		Homeservers: []b.Homeserver{
			{
				Name: "hs1",
				Users: []b.User{
					{
						Localpart:   "@alice",
						DisplayName: "Alice",
					},
					{
						Localpart:   "@bob",
						DisplayName: "Bob",
					},
					{
						Localpart:   "@charlie",
						DisplayName: "Charlie",
					},
				},
			},
		},
	}))
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")
	charlie := deployment.Client(t, "hs1", "@charlie:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	bob.JoinRoom(t, roomID, nil)

	alice.SendEventSynced(t, roomID, b.Event{
		Type:     "m.room.power_levels",
		StateKey: b.Ptr(""),
		Content: map[string]interface{}{
			"users": map[string]interface{}{
				charlie.UserID: 100,
			},
		},
	})

	// todo: replace with `SyncUntilJoined`
	bob.SyncUntilTimelineHas(t, roomID, func(event gjson.Result) bool {
		return event.Get("type").Str == "m.room.member" &&
			event.Get("content.membership").Str == "join" &&
			event.Get("state_key").Str == bob.UserID
	})

	res := charlie.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "rooms", roomID, "ban"}, client.WithJSONBody(t, map[string]interface{}{
		"user_id": bob.UserID,
		"reason":  "testing",
	}))

	if res.StatusCode != 403 {
		defer res.Body.Close()
		body, _ := ioutil.ReadAll(res.Body)
		t.Fatalf("Banning a user succeeded where it shouldn't: (%d) %s", res.StatusCode, string(body))
	}
}
