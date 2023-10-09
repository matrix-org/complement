package csapi_tests

import (
	"net/url"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

func TestLeftRoomFixture(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")
	charlie := deployment.RegisterUser(t, "hs1", "charlie", "sufficiently_long_password_charlie", false)

	roomID := alice.CreateRoom(t, map[string]interface{}{
		"initial_state": []map[string]interface{}{
			{
				"content": map[string]interface{}{
					"history_visibility": "joined",
				},
				"type":      "m.room.history_visibility",
				"state_key": "",
			},
		},
		"preset": "public_chat",
	})

	bob.MustJoinRoom(t, roomID, nil)

	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	const madeUpStateKey = "madeup.test.state"

	const (
		beforeRoomName    = "N1. B's room name before A left"
		beforeMadeUpState = "S1. B's state before A left"
		beforeMessageOne  = "M1. B's message before A left"
		beforeMessageTwo  = "M2. B's message before A left"
	)

	const (
		afterRoomName    = "N2. B's room name after A left"
		afterMadeUpState = "S2. B's state after A left"
		afterMessage     = "M3. B's message after A left"
	)

	alice.SendEventSynced(t, roomID, b.Event{
		Type:     "m.room.name",
		StateKey: b.Ptr(""),
		Content: map[string]interface{}{
			"name": beforeRoomName,
		},
	})

	alice.SendEventSynced(t, roomID, b.Event{
		Type:     madeUpStateKey,
		StateKey: b.Ptr(""),
		Content: map[string]interface{}{
			"body": beforeMadeUpState,
		},
	})

	alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    beforeMessageOne,
		},
	})

	alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    beforeMessageTwo,
		},
	})

	bob.LeaveRoom(t, roomID)

	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(bob.UserID, roomID))

	_, bobSinceToken := bob.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

	alice.SendEventSynced(t, roomID, b.Event{
		Type:     "m.room.name",
		StateKey: b.Ptr(""),
		Content: map[string]interface{}{
			"name": afterRoomName,
		},
	})

	alice.SendEventSynced(t, roomID, b.Event{
		Type:     madeUpStateKey,
		StateKey: b.Ptr(""),
		Content: map[string]interface{}{
			"body": afterMadeUpState,
		},
	})

	alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    afterMessage,
		},
	})

	// Have charlie join the room, to check against /members calls later
	// (Bob should not see Charlie in /members after he leaves the room.)
	charlie.MustJoinRoom(t, roomID, nil)
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(charlie.UserID, roomID))

	// sytest: Can get rooms/{roomId}/state for a departed room (SPEC-216)
	t.Run("Can get rooms/{roomId}/state for a departed room", func(t *testing.T) {
		// Bob gets the old state
		resp := bob.MustDo(
			t,
			"GET",
			[]string{"_matrix", "client", "v3", "rooms", roomID, "state", madeUpStateKey},
		)
		must.MatchResponse(t, resp, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("body", beforeMadeUpState),
			},
		})

		// ...While Alice gets the new state
		resp = alice.MustDo(
			t,
			"GET",
			[]string{"_matrix", "client", "v3", "rooms", roomID, "state", madeUpStateKey},
		)
		must.MatchResponse(t, resp, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("body", afterMadeUpState),
			},
		})
	})

	// sytest: Can get rooms/{roomId}/members for a departed room (SPEC-216)
	t.Run("Can get rooms/{roomId}/members for a departed room", func(t *testing.T) {
		resp := bob.MustDo(
			t,
			"GET",
			[]string{"_matrix", "client", "v3", "rooms", roomID, "members"},
		)

		must.MatchResponse(t, resp, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONCheckOff("chunk",
					[]interface{}{
						"m.room.member|" + alice.UserID + "|join",
						"m.room.member|" + bob.UserID + "|leave",
					}, func(result gjson.Result) interface{} {
						return strings.Join([]string{
							result.Map()["type"].Str,
							result.Map()["state_key"].Str,
							result.Get("content.membership").Str,
						}, "|")
					}, nil),
			},
		})
	})

	// sytest: Can get rooms/{roomId}/messages for a departed room (SPEC-216)
	t.Run("Can get rooms/{roomId}/messages for a departed room", func(t *testing.T) {
		resp := bob.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(url.Values{
			"dir":   []string{"b"},
			"limit": []string{"3"},
			"from":  []string{bobSinceToken},
		}))

		must.MatchResponse(t, resp, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONCheckOff("chunk", []interface{}{
					"m.room.message|" + beforeMessageOne + "|",
					"m.room.message|" + beforeMessageTwo + "|",
					"m.room.member||" + bob.UserID,
				}, func(result gjson.Result) interface{} {
					return strings.Join([]string{
						result.Map()["type"].Str,
						result.Get("content.body").Str,
						result.Map()["state_key"].Str,
					}, "|")
				}, nil),
			},
		})
	})

	// sytest: Can get 'm.room.name' state for a departed room (SPEC-216)
	t.Run("Can get 'm.room.name' state for a departed room", func(t *testing.T) {
		// Bob gets the old name
		resp := bob.MustDo(
			t,
			"GET",
			[]string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.name"},
		)
		must.MatchResponse(t, resp, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("name", beforeRoomName),
			},
		})

		// ...While Alice gets the new name
		resp = alice.MustDo(
			t,
			"GET",
			[]string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.name"},
		)
		must.MatchResponse(t, resp, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("name", afterRoomName),
			},
		})
	})

	// sytest: Getting messages going forward is limited for a departed room (SPEC-216)
	t.Run("Getting messages going forward is limited for a departed room", func(t *testing.T) {
		// TODO: try this with the most recent since token too
		resp := bob.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(url.Values{
			"dir":   []string{"f"},
			"limit": []string{"100"},
			"from":  []string{bobSinceToken},
		}))

		must.MatchResponse(t, resp, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("chunk", 0),
			},
		})
	})

}
