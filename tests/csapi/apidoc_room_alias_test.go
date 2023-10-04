package csapi_tests

import (
	"net/http"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func setRoomAliasResp(t *testing.T, c *client.CSAPI, roomID, roomAlias string) *http.Response {
	return c.Do(t, "PUT", []string{"_matrix", "client", "v3", "directory", "room", roomAlias}, client.WithJSONBody(t, map[string]interface{}{
		"room_id": roomID,
	}))
}

func getRoomAliasResp(t *testing.T, c *client.CSAPI, roomAlias string) *http.Response {
	return c.Do(t, "GET", []string{"_matrix", "client", "v3", "directory", "room", roomAlias})
}

func deleteRoomAliasResp(t *testing.T, c *client.CSAPI, roomAlias string) *http.Response {
	return c.Do(t, "DELETE", []string{"_matrix", "client", "v3", "directory", "room", roomAlias})
}

func listRoomAliasesResp(t *testing.T, c *client.CSAPI, roomID string) *http.Response {
	return c.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "aliases"})
}

func setCanonicalAliasResp(t *testing.T, c *client.CSAPI, roomID string, roomAlias string, altAliases *[]string) *http.Response {
	content := map[string]interface{}{
		"alias": roomAlias,
	}
	if altAliases != nil {
		content["alt_aliases"] = altAliases
	}

	return c.Do(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.canonical_alias"}, client.WithJSONBody(t, content))
}

func mustSetCanonicalAlias(t *testing.T, c *client.CSAPI, roomID string, roomAlias string, altAliases *[]string) string {
	content := map[string]interface{}{
		"alias": roomAlias,
	}
	if altAliases != nil {
		content["alt_aliases"] = altAliases
	}

	return c.SendEventSynced(t, roomID, b.Event{
		Type:     "m.room.canonical_alias",
		StateKey: b.Ptr(""),
		Content:  content,
	})
}

func TestRoomAlias(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	t.Run("Parallel", func(t *testing.T) {
		// sytest: PUT /directory/room/:room_alias creates alias
		t.Run("PUT /directory/room/:room_alias creates alias", func(t *testing.T) {
			t.Parallel()

			roomID := alice.CreateRoom(t, map[string]interface{}{})

			roomAlias := "#creates_alias:hs1"

			setRoomAliasResp(t, alice, roomID, roomAlias)

			res := getRoomAliasResp(t, alice, roomAlias)

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyEqual("room_id", roomID),
				},
			})
		})

		// sytest: GET /rooms/:room_id/aliases lists aliases
		t.Run("GET /rooms/:room_id/aliases lists aliases", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{})

			res := listRoomAliasesResp(t, alice, roomID)

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyEqual("aliases", []interface{}{}),
				},
			})

			roomAlias := "#lists_aliases:hs1"

			setRoomAliasResp(t, alice, roomID, roomAlias)

			// Synapse doesn't read-after-write consistency here; it can race:
			//
			// 1. Request to set the alias arrives on a writer worker.
			// 2. Writer tells the reader worker to invalidate its caches.
			// 3. Response received by the client.
			// 4. A new query arrives at the reader.
			//
			// If (4) arrives at the reader before (2), the reader responds with
			// old data. Bodge around this by retrying for up to a second.
			res = alice.Do(
				t,
				"GET",
				[]string{"_matrix", "client", "v3", "rooms", roomID, "aliases"},
				client.WithRetryUntil(
					1*time.Second,
					func(res *http.Response) bool {
						if res.StatusCode != 200 {
							return false
						}
						eventResBody := client.ParseJSON(t, res)
						matcher := match.JSONKeyEqual("aliases", []interface{}{roomAlias})
						err := matcher(eventResBody)
						if err != nil {
							t.Log(err)
							return false
						}
						return true
					},
				),
			)
		})

		// sytest: Only room members can list aliases of a room
		t.Run("Only room members can list aliases of a room", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{})

			roomAlias := "#room_members_list:hs1"

			res := setRoomAliasResp(t, alice, roomID, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			// An extra check to make sure we're not being racy rn
			res = getRoomAliasResp(t, alice, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyEqual("room_id", roomID),
				},
			})

			res = listRoomAliasesResp(t, bob, roomID)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 403,
			})
		})

		// sytest: Room aliases can contain Unicode
		t.Run("Room aliases can contain Unicode", func(t *testing.T) {
			t.Parallel()

			const unicodeAlias = "#老虎Â£я🤨👉ඞ:hs1"

			roomID := alice.CreateRoom(t, map[string]interface{}{})

			res := setRoomAliasResp(t, alice, roomID, unicodeAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			res = getRoomAliasResp(t, alice, unicodeAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyEqual("room_id", roomID),
				},
			})
		})
	})
}

func TestRoomDeleteAlias(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	t.Run("Parallel", func(t *testing.T) {
		// sytest: Alias creators can delete alias with no ops
		t.Run("Alias creators can delete alias with no ops", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "public_chat",
			})

			bob.JoinRoom(t, roomID, nil)
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

			roomAlias := "#no_ops_delete:hs1"

			res := setRoomAliasResp(t, bob, roomID, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			// An extra check to make sure we're not being racy rn
			res = getRoomAliasResp(t, bob, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyEqual("room_id", roomID),
				},
			})

			res = deleteRoomAliasResp(t, bob, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})
		})

		// sytest: Alias creators can delete canonical alias with no ops
		t.Run("Alias creators can delete canonical alias with no ops", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "public_chat",
			})

			bob.JoinRoom(t, roomID, nil)
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

			roomAlias := "#no_ops_delete_canonical:hs1"

			res := setRoomAliasResp(t, bob, roomID, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			// An extra check to make sure we're not being racy rn
			res = getRoomAliasResp(t, bob, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyEqual("room_id", roomID),
				},
			})

			mustSetCanonicalAlias(t, alice, roomID, roomAlias, nil)

			res = deleteRoomAliasResp(t, bob, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})
		})

		// sytest: Deleting a non-existent alias should return a 404
		t.Run("Deleting a non-existent alias should return a 404", func(t *testing.T) {
			t.Parallel()

			roomAlias := "#scatman_portal:hs1"

			res := deleteRoomAliasResp(t, bob, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 404,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_NOT_FOUND"),
				},
			})
		})

		// sytest: Can delete canonical alias
		t.Run("Can delete canonical alias", func(t *testing.T) {
			t.Parallel()

			roomID := alice.CreateRoom(t, map[string]interface{}{})

			roomAlias := "#random_alias:hs1"

			res := setRoomAliasResp(t, alice, roomID, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			mustSetCanonicalAlias(t, alice, roomID, roomAlias, nil)

			_, sinceToken := alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

			res = deleteRoomAliasResp(t, alice, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			alice.MustSyncUntil(t, client.SyncReq{Since: sinceToken}, client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
				if ev.Get("type").Str == "m.room.canonical_alias" &&
					ev.Get("content").IsObject() &&
					len(ev.Get("content").Map()) == 0 {
					return true
				}

				return false
			}))
		})

		// sytest: Regular users can add and delete aliases in the default room configuration
		t.Run("Regular users can add and delete aliases in the default room configuration", func(t *testing.T) {
			t.Parallel()

			roomID := alice.CreateRoom(t, map[string]interface{}{})

			randomAlias := "#random_alias_2:hs1"

			alice.InviteRoom(t, roomID, bob.UserID)
			bob.JoinRoom(t, roomID, nil)
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

			res := setRoomAliasResp(t, bob, roomID, randomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			res = getRoomAliasResp(t, alice, randomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyEqual("room_id", roomID),
				},
			})

			res = deleteRoomAliasResp(t, bob, randomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})
		})

		// sytest: Regular users can add and delete aliases when m.room.aliases is restricted
		t.Run("Regular users can add and delete aliases when m.room.aliases is restricted", func(t *testing.T) {
			t.Parallel()

			roomID := alice.CreateRoom(t, map[string]interface{}{})

			randomAlias := "#random_alias_3:hs1"

			alice.InviteRoom(t, roomID, bob.UserID)
			bob.JoinRoom(t, roomID, nil)

			alice.SendEventSynced(t, roomID, b.Event{
				Type:     "m.room.power_levels",
				StateKey: b.Ptr(""),
				Content: map[string]interface{}{
					"users": map[string]int64{
						alice.UserID: 100,
					},
					"events": map[string]int64{
						"m.room.aliases": 50,
					},
				},
			})

			res := setRoomAliasResp(t, bob, roomID, randomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			res = getRoomAliasResp(t, alice, randomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyEqual("room_id", roomID),
				},
			})

			res = deleteRoomAliasResp(t, bob, randomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})
		})

		// sytest: Users can't delete other's aliases
		t.Run("Users can't delete other's aliases", func(t *testing.T) {
			t.Parallel()

			roomID := alice.CreateRoom(t, map[string]interface{}{})

			randomAlias := "#random_alias_4:hs1"

			alice.InviteRoom(t, roomID, bob.UserID)
			bob.JoinRoom(t, roomID, nil)

			res := setRoomAliasResp(t, alice, roomID, randomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			res = getRoomAliasResp(t, bob, randomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyEqual("room_id", roomID),
				},
			})

			res = deleteRoomAliasResp(t, bob, randomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 403,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_FORBIDDEN"),
				},
			})
		})

		// sytest: Users with sufficient power-level can delete other's aliases
		t.Run("Users with sufficient power-level can delete other's aliases", func(t *testing.T) {
			t.Parallel()

			roomID := alice.CreateRoom(t, map[string]interface{}{})

			randomAlias := "#random_alias_5:hs1"

			alice.InviteRoom(t, roomID, bob.UserID)
			bob.JoinRoom(t, roomID, nil)

			alice.SendEventSynced(t, roomID, b.Event{
				Type:     "m.room.power_levels",
				StateKey: b.Ptr(""),
				Content: map[string]interface{}{
					"users": map[string]int64{
						alice.UserID: 100,
						bob.UserID:   100,
					},
				},
			})

			res := setRoomAliasResp(t, alice, roomID, randomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			res = getRoomAliasResp(t, bob, randomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyEqual("room_id", roomID),
				},
			})

			res = deleteRoomAliasResp(t, bob, randomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})
		})
	})
}

func TestRoomCanonicalAlias(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")

	t.Run("Parallel", func(t *testing.T) {

		// The original sytest has been split out into three tests, the test name only pertained to the first.
		// sytest: Canonical alias can be set
		t.Run("m.room.canonical_alias accepts present aliases", func(t *testing.T) {

			roomID := alice.CreateRoom(t, map[string]interface{}{})

			t.Parallel()

			roomAlias := "#accepts_present_aliases:hs1"

			res := setRoomAliasResp(t, alice, roomID, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			mustSetCanonicalAlias(t, alice, roomID, roomAlias, nil)
		})

		// part of "Canonical alias can be set"
		t.Run("m.room.canonical_alias rejects missing aliases", func(t *testing.T) {

			roomID := alice.CreateRoom(t, map[string]interface{}{})

			t.Parallel()

			roomAlias := "#rejects_missing:hs1"

			res := setCanonicalAliasResp(t, alice, roomID, roomAlias, nil)

			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 400,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_BAD_ALIAS"),
				},
			})
		})

		// part of "Canonical alias can be set"
		t.Run("m.room.canonical_alias rejects invalid aliases", func(t *testing.T) {

			roomID := alice.CreateRoom(t, map[string]interface{}{})

			t.Parallel()

			roomAlias := "%invalid_aliases:hs1"

			res := setCanonicalAliasResp(t, alice, roomID, roomAlias, nil)

			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 400,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_INVALID_PARAM"),
				},
			})
		})

		t.Run("m.room.canonical_alias setting rejects deleted aliases", func(t *testing.T) {

			roomID := alice.CreateRoom(t, map[string]interface{}{})

			t.Parallel()

			roomAlias := "#deleted_aliases:hs1"

			res := setRoomAliasResp(t, alice, roomID, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			res = deleteRoomAliasResp(t, alice, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			// An extra check to make sure we're not being racy rn
			res = getRoomAliasResp(t, alice, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 404,
			})

			res = setCanonicalAliasResp(t, alice, roomID, roomAlias, nil)

			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 400,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_BAD_ALIAS"),
				},
			})
		})

		t.Run("m.room.canonical_alias rejects alias pointing to different local room", func(t *testing.T) {

			room1 := alice.CreateRoom(t, map[string]interface{}{})
			room2 := alice.CreateRoom(t, map[string]interface{}{})

			t.Parallel()

			roomAlias := "#diffroom1:hs1"

			res := setRoomAliasResp(t, alice, room1, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			res = setCanonicalAliasResp(t, alice, room2, roomAlias, nil)

			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 400,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_BAD_ALIAS"),
				},
			})
		})

		// The original sytest has been split out into three tests, the test name only pertained to the first.
		// sytest: Canonical alias can include alt_aliases
		t.Run("m.room.canonical_alias accepts present alt_aliases", func(t *testing.T) {
			roomID := alice.CreateRoom(t, map[string]interface{}{})

			t.Parallel()

			roomAlias := "#alt_present_alias:hs1"

			res := setRoomAliasResp(t, alice, roomID, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			mustSetCanonicalAlias(t, alice, roomID, roomAlias, &[]string{roomAlias})
		})

		// part of "Canonical alias can include alt_aliases"
		t.Run("m.room.canonical_alias rejects missing aliases", func(t *testing.T) {
			roomID := alice.CreateRoom(t, map[string]interface{}{})

			t.Parallel()

			roomAlias := "#alt_missing:hs1"
			wrongRoomAlias := "#alt_missing_wrong:hs1"

			res := setRoomAliasResp(t, alice, roomID, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			res = setCanonicalAliasResp(t, alice, roomID, roomAlias, &[]string{wrongRoomAlias})

			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 400,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_BAD_ALIAS"),
				},
			})
		})

		// part of "Canonical alias can include alt_aliases"
		t.Run("m.room.canonical_alias rejects invalid aliases", func(t *testing.T) {
			roomID := alice.CreateRoom(t, map[string]interface{}{})

			t.Parallel()

			roomAlias := "#alt_invalid:hs1"
			wrongRoomAlias := "%alt_invalid_wrong:hs1"

			res := setRoomAliasResp(t, alice, roomID, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			res = setCanonicalAliasResp(t, alice, roomID, roomAlias, &[]string{wrongRoomAlias})

			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 400,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_INVALID_PARAM"),
				},
			})
		})

		// part of "Canonical alias can include alt_aliases"
		t.Run("m.room.canonical_alias rejects alt_alias pointing to different local room", func(t *testing.T) {

			room1 := alice.CreateRoom(t, map[string]interface{}{})
			room2 := alice.CreateRoom(t, map[string]interface{}{})

			t.Parallel()

			room1Alias := "#alt_room1:hs1"
			room2Alias := "#alt_room2:hs1"

			res := setRoomAliasResp(t, alice, room1, room1Alias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			res = setRoomAliasResp(t, alice, room2, room2Alias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			res = setCanonicalAliasResp(t, alice, room2, room2Alias, &[]string{room1Alias})

			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 400,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_BAD_ALIAS"),
				},
			})
		})
	})
}
