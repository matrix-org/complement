package csapi_tests

import (
	"net/url"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

// Maps every object by extracting `type` and `state_key` into a "$type|$state_key" string.
func typeToStateKeyMapper(result gjson.Result) interface{} {
	return strings.Join([]string{result.Map()["type"].Str, result.Map()["state_key"].Str}, "|")
}

// sytest: Can get rooms/{roomId}/members
func TestGetRoomMembers(t *testing.T) {
	deployment := complement.Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	bob.MustJoinRoom(t, roomID, nil)

	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	resp := alice.MustDo(
		t,
		"GET",
		[]string{"_matrix", "client", "v3", "rooms", roomID, "members"},
	)

	must.MatchResponse(t, resp, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONArrayEach("chunk.#.room_id", func(result gjson.Result) error {
				must.Equal(t, result.Str, roomID, "unexpected roomID")
				return nil
			}),
			match.JSONCheckOff("chunk",
				[]interface{}{
					"m.room.member|" + alice.UserID,
					"m.room.member|" + bob.UserID,
				}, typeToStateKeyMapper, nil),
		},
		StatusCode: 200,
	})
}

// Utilize ?at= to get room members at a point in sync.
// sytest: Can get rooms/{roomId}/members at a given point
func TestGetRoomMembersAtPoint(t *testing.T) {
	deployment := complement.Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world!",
		},
	})

	syncResp, _ := alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})
	sinceToken := syncResp.Get("rooms.join." + client.GjsonEscape(roomID) + ".timeline.prev_batch").Str

	bob.MustJoinRoom(t, roomID, nil)
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	bob.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello back",
		},
	})

	resp := alice.MustDo(
		t,
		"GET",
		[]string{"_matrix", "client", "v3", "rooms", roomID, "members"},
		client.WithQueries(url.Values{
			"at": []string{sinceToken},
		}),
	)

	must.MatchResponse(t, resp, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONArrayEach("chunk.#.room_id", func(result gjson.Result) error {
				must.Equal(t, result.Str, roomID, "unexpected roomID")
				return nil
			}),
			match.JSONCheckOff("chunk",
				[]interface{}{
					"m.room.member|" + alice.UserID,
				}, typeToStateKeyMapper, nil),
		},

		StatusCode: 200,
	})
}

// sytest: Can filter rooms/{roomId}/members
func TestGetFilteredRoomMembers(t *testing.T) {

	deployment := complement.Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	bob.MustJoinRoom(t, roomID, nil)

	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	bob.MustLeaveRoom(t, roomID)

	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(bob.UserID, roomID))

	t.Run("not_membership", func(t *testing.T) {
		resp := alice.MustDo(
			t,
			"GET",
			[]string{"_matrix", "client", "v3", "rooms", roomID, "members"},
			client.WithQueries(url.Values{
				"not_membership": []string{"leave"},
			}),
		)

		must.MatchResponse(t, resp, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONArrayEach("chunk.#.room_id", func(result gjson.Result) error {
					must.Equal(t, result.Str, roomID, "unexpected roomID")
					return nil
				}),
				match.JSONCheckOff("chunk",
					[]interface{}{
						"m.room.member|" + alice.UserID,
					}, typeToStateKeyMapper, nil),
			},
			StatusCode: 200,
		})
	})

	t.Run("membership/leave", func(t *testing.T) {
		resp := alice.MustDo(
			t,
			"GET",
			[]string{"_matrix", "client", "v3", "rooms", roomID, "members"},
			client.WithQueries(url.Values{
				"membership": []string{"leave"},
			}),
		)

		must.MatchResponse(t, resp, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONArrayEach("chunk.#.room_id", func(result gjson.Result) error {
					must.Equal(t, result.Str, roomID, "unexpected roomID")
					return nil
				}),
				match.JSONCheckOff("chunk",
					[]interface{}{
						"m.room.member|" + bob.UserID,
					}, typeToStateKeyMapper, nil),
			},
			StatusCode: 200,
		})
	})

	t.Run("membership/join", func(t *testing.T) {
		resp := alice.MustDo(
			t,
			"GET",
			[]string{"_matrix", "client", "v3", "rooms", roomID, "members"},
			client.WithQueries(url.Values{
				"membership": []string{"join"},
			}),
		)

		must.MatchResponse(t, resp, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONArrayEach("chunk.#.room_id", func(result gjson.Result) error {
					must.Equal(t, result.Str, roomID, "unexpected roomID")
					return nil
				}),
				match.JSONCheckOff("chunk",
					[]interface{}{
						"m.room.member|" + alice.UserID,
					}, typeToStateKeyMapper, nil),
			},
			StatusCode: 200,
		})
	})
}
