package csapi_tests

import (
	"net/http"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/matrix-org/complement/runtime"
)

func setRoomAliasResp(t *testing.T, c *client.CSAPI, roomID, roomAlias string) *http.Response {
	return c.DoFunc(t, "PUT", []string{"_matrix", "client", "r0", "directory", "room", roomAlias}, client.WithJSONBody(t, map[string]interface{}{
		"room_id": roomID,
	}))
}

func getRoomAliasResp(t *testing.T, c *client.CSAPI, roomAlias string) *http.Response {
	return c.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "directory", "room", roomAlias})
}

func deleteRoomAliasResp(t *testing.T, c *client.CSAPI, roomAlias string) *http.Response {
	return c.DoFunc(t, "DELETE", []string{"_matrix", "client", "r0", "directory", "room", roomAlias})
}

func listRoomAliasesResp(t *testing.T, c *client.CSAPI, roomID string) *http.Response {
	return c.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "aliases"})
}

func setCanonicalAlias(t *testing.T, c *client.CSAPI, roomID string, roomAlias string, altAliases *[]string) *http.Response {
	content := map[string]interface{}{
		"alias": roomAlias,
	}
	if altAliases != nil {
		content["alt_aliases"] = altAliases
	}

	return c.DoFunc(t, "PUT", []string{"_matrix", "client", "r0", "rooms", roomID, "state", "m.room.canonical_alias"}, client.WithJSONBody(t, content))
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

			res = listRoomAliasesResp(t, alice, roomID)

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyEqual("aliases", []interface{}{roomAlias}),
				},
			})
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

			res = setCanonicalAlias(t, alice, roomID, roomAlias, nil)

			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyPresent("event_id"),
				},
			})

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

			res = setCanonicalAlias(t, alice, roomID, roomAlias, nil)

			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyPresent("event_id"),
				},
			})
		})

		// part of "Canonical alias can be set"
		t.Run("m.room.canonical_alias rejects missing aliases", func(t *testing.T) {
			runtime.SkipIf(t, runtime.Dendrite) // FIXME https://github.com/matrix-org/dendrite/issues/2068

			roomID := alice.CreateRoom(t, map[string]interface{}{})

			t.Parallel()

			roomAlias := "#rejects_missing:hs1"

			res := setCanonicalAlias(t, alice, roomID, roomAlias, nil)

			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 400,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_BAD_ALIAS"),
				},
			})
		})

		// part of "Canonical alias can be set"
		t.Run("m.room.canonical_alias rejects invalid aliases", func(t *testing.T) {
			runtime.SkipIf(t, runtime.Dendrite) // FIXME https://github.com/matrix-org/dendrite/issues/2069

			roomID := alice.CreateRoom(t, map[string]interface{}{})

			t.Parallel()

			roomAlias := "%invalid_aliases:hs1"

			res := setCanonicalAlias(t, alice, roomID, roomAlias, nil)

			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 400,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_INVALID_PARAM"),
				},
			})
		})

		t.Run("m.room.canonical_alias setting rejects deleted aliases", func(t *testing.T) {
			runtime.SkipIf(t, runtime.Dendrite) // FIXME https://github.com/matrix-org/dendrite/issues/2068

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

			res = setCanonicalAlias(t, alice, roomID, roomAlias, nil)

			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 400,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_BAD_ALIAS"),
				},
			})
		})

		t.Run("m.room.canonical_alias rejects alias pointing to different local room", func(t *testing.T) {
			runtime.SkipIf(t, runtime.Dendrite) // FIXME https://github.com/matrix-org/dendrite/issues/2068

			room1 := alice.CreateRoom(t, map[string]interface{}{})
			room2 := alice.CreateRoom(t, map[string]interface{}{})

			t.Parallel()

			roomAlias := "#diffroom1:hs1"

			res := setRoomAliasResp(t, alice, room1, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			res = setCanonicalAlias(t, alice, room2, roomAlias, nil)

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

			res = setCanonicalAlias(t, alice, roomID, roomAlias, &[]string{roomAlias})

			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
				JSON: []match.JSON{
					match.JSONKeyPresent("event_id"),
				},
			})
		})

		// part of "Canonical alias can include alt_aliases"
		t.Run("m.room.canonical_alias rejects missing aliases", func(t *testing.T) {
			runtime.SkipIf(t, runtime.Dendrite) // FIXME https://github.com/matrix-org/dendrite/issues/2068
			roomID := alice.CreateRoom(t, map[string]interface{}{})

			t.Parallel()

			roomAlias := "#alt_missing:hs1"
			wrongRoomAlias := "#alt_missing_wrong:hs1"

			res := setRoomAliasResp(t, alice, roomID, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			res = setCanonicalAlias(t, alice, roomID, roomAlias, &[]string{wrongRoomAlias})

			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 400,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_BAD_ALIAS"),
				},
			})
		})

		// part of "Canonical alias can include alt_aliases"
		t.Run("m.room.canonical_alias rejects invalid aliases", func(t *testing.T) {
			runtime.SkipIf(t, runtime.Dendrite) // FIXME https://github.com/matrix-org/dendrite/issues/2069
			roomID := alice.CreateRoom(t, map[string]interface{}{})

			t.Parallel()

			roomAlias := "#alt_invalid:hs1"
			wrongRoomAlias := "%alt_invalid_wrong:hs1"

			res := setRoomAliasResp(t, alice, roomID, roomAlias)
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 200,
			})

			res = setCanonicalAlias(t, alice, roomID, roomAlias, &[]string{wrongRoomAlias})

			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 400,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_INVALID_PARAM"),
				},
			})
		})

		// part of "Canonical alias can include alt_aliases"
		t.Run("m.room.canonical_alias rejects alt_alias pointing to different local room", func(t *testing.T) {
			runtime.SkipIf(t, runtime.Dendrite) // FIXME https://github.com/matrix-org/dendrite/issues/2068

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

			res = setCanonicalAlias(t, alice, room2, room2Alias, &[]string{room1Alias})

			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 400,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_BAD_ALIAS"),
				},
			})
		})
	})
}
