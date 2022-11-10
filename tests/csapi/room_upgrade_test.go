package csapi_tests

import (
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

// sytest: /upgrade creates a new room
func TestUpgradeRoom(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	_, tombstoneSinceToken := alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})
	_, sinceToken := alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

	resp := alice.MustDoFunc(
		t,
		"POST",
		[]string{"_matrix", "client", "v3", "rooms", roomID, "upgrade"},
		client.WithJSONBody(t, map[string]string{
			"new_version": "6",
		}),
	)

	upgradeJson := gjson.ParseBytes(must.ParseJSON(t, resp.Body))
	must.MatchGJSON(
		t,
		upgradeJson,
		match.JSONKeyPresent("replacement_room"),
		match.JSONKeyTypeEqual("replacement_room", gjson.String),
	)
	replacementRoom := upgradeJson.Get("replacement_room").Str

	if roomID == replacementRoom {
		t.Fatalf("Rooms are the same (%s)", roomID)
	}

	// Check for and fetch the tombstone event ID
	tombstoneEventId := ""
	alice.MustSyncUntil(t, client.SyncReq{Since: tombstoneSinceToken}, client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
		if ev.Get("type").Str == "m.room.tombstone" && ev.Get("state_key").Exists() {
			repRoomRes := ev.Get("content.replacement_room")

			if repRoomRes.Exists() {
				tombstoneEventId = ev.Get("event_id").Str

				if repRoomRes.Str != replacementRoom {
					t.Errorf("tombstone event did not match replacement room, got %s, want %s", repRoomRes.Str, replacementRoom)
				}

				return true
			}
		}

		return false
	}))

	// Convenience method to check for state event
	stateExists := func(evType string) func(gjson.Result) bool {
		return func(ev gjson.Result) bool {
			return ev.Get("type").Str == evType && ev.Get("state_key").Exists()
		}
	}

	// Note: we cannot check for the following state here, as it's only prescribed as an "implementation detail" by the spec:
	// - m.room.guest_access
	// - m.room.history_visibility
	// - m.room.join_rules
	alice.MustSyncUntil(t, client.SyncReq{Since: sinceToken}, client.SyncJoinedTo(alice.UserID, replacementRoom),
		client.SyncTimelineHas(replacementRoom, stateExists("m.room.create")),
		client.SyncTimelineHas(replacementRoom, stateExists("m.room.member")),
		client.SyncTimelineHas(replacementRoom, stateExists("m.room.power_levels")),

		client.SyncTimelineHas(replacementRoom, func(ev gjson.Result) bool {
			if ev.Get("type").Str == "m.room.create" && ev.Get("state_key").Exists() {
				predecessor := ev.Get("content.predecessor")

				if predecessor.Get("room_id").Exists() && predecessor.Get("event_id").Exists() {
					if predecessor.Get("room_id").Str != roomID {
						t.Errorf("predecessor room id did not match: got %s, want %s", predecessor.Get("room_id").Str, roomID)
					}

					if predecessor.Get("event_id").Str != tombstoneEventId {
						t.Errorf("predecessor tombstone event id did not match: got %s, want %s", predecessor.Get("event_id").Str, tombstoneEventId)
					}

					return true
				}
			}
			return false
		}),
	)
}

// sytest: /upgrade moves aliases to the new room
func TestUpgradeMovesAliases(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	const localRoomAliasMain = "#room_upgrade_local1_test:hs1"
	const localRoomAliasAlt = "#room_upgrade_local2_test:hs1"

	alice.MustDoFunc(
		t,
		"PUT",
		[]string{"_matrix", "client", "v3", "directory", "room", localRoomAliasMain},
		client.WithJSONBody(t, map[string]interface{}{
			"room_id": roomID,
		}),
	)

	alice.MustDoFunc(
		t,
		"PUT",
		[]string{"_matrix", "client", "v3", "directory", "room", localRoomAliasAlt},
		client.WithJSONBody(t, map[string]interface{}{
			"room_id": roomID,
		}),
	)

	alice.SendEventSynced(t, roomID, b.Event{
		Type:     "m.room.canonical_alias",
		StateKey: b.Ptr(""),
		Content: map[string]interface{}{
			"alias":       localRoomAliasMain,
			"alt_aliases": []interface{}{localRoomAliasAlt},
		},
	})

	_, sinceToken := alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})

	resp := alice.MustDoFunc(
		t,
		"POST",
		[]string{"_matrix", "client", "v3", "rooms", roomID, "upgrade"},
		client.WithJSONBody(t, map[string]string{
			"new_version": "6",
		}),
	)

	upgradeJson := gjson.ParseBytes(must.ParseJSON(t, resp.Body))
	must.MatchGJSON(
		t,
		upgradeJson,
		match.JSONKeyPresent("replacement_room"),
		match.JSONKeyTypeEqual("replacement_room", gjson.String),
	)
	replacementRoom := upgradeJson.Get("replacement_room").Str

	alice.MustSyncUntil(t, client.SyncReq{Since: sinceToken}, client.SyncJoinedTo(alice.UserID, replacementRoom),
		// Get the canonical alias from the old room, assert it is empty
		client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
			if ev.Get("type").Str == "m.room.canonical_alias" && ev.Get("state_key").Exists() {
				content := ev.Get("content")

				if content.Exists() && len(content.Map()) == 0 {
					return true
				}
			}
			return false
		}),
		// Get the canonical alias from the new room, assert it is filled with content from the old alias
		client.SyncTimelineHas(replacementRoom, func(ev gjson.Result) bool {
			if ev.Get("type").Str == "m.room.canonical_alias" && ev.Get("state_key").Exists() {
				content := ev.Get("content")

				if content.Get("alias").Exists() && content.Get("alias").Str == localRoomAliasMain &&
					content.Get("alt_aliases").Exists() && content.Get("alt_aliases").IsArray() {

					altAliases := content.Get("alt_aliases").Value().([]interface{})

					if len(altAliases) == 1 && altAliases[0] == localRoomAliasAlt {
						return true
					}
				}
			}
			return false
		}),
	)

	for _, alias := range []string{localRoomAliasMain, localRoomAliasAlt} {
		// We try to fetch the new alias 10 times, each with 1s backoff
		for i := 0; i < 10; i++ {
			resp := alice.DoFunc(t, "GET", []string{"_matrix", "client", "v3", "directory", "room", alias})

			if resp.StatusCode == 200 {
				aliasJson := gjson.ParseBytes(must.ParseJSON(t, resp.Body))

				aliasRoomIdRes := aliasJson.Get("room_id")

				if aliasRoomIdRes.Exists() && aliasRoomIdRes.Type == gjson.String {
					aliasRoomId := aliasRoomIdRes.Str

					if aliasRoomId != replacementRoom {
						t.Logf("room_id %s did not match replacement room %s", aliasRoomId, replacementRoom)
					} else {
						break
					}
				}
			}

			if i == 9 {
				t.Error("room_id did not match replacement room after 10 tries")
			} else {
				t.Logf("try %d failed, sleeping 1 second...", i+1)
				time.Sleep(1 * time.Second)
			}
		}
	}
}

// sytest: /upgrade is rejected if the user can't send state events
func TestUpgradeRoomFailsNoPermission(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	roomID := alice.CreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	bob.JoinRoom(t, roomID, nil)
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	resp := bob.DoFunc(
		t,
		"POST",
		[]string{"_matrix", "client", "v3", "rooms", roomID, "upgrade"},
		client.WithJSONBody(t, map[string]string{
			"new_version": "6",
		}),
	)

	must.MatchResponse(t, resp, match.HTTPResponse{
		StatusCode: 403,
		JSON: []match.JSON{
			match.JSONKeyEqual("errcode", "M_FORBIDDEN"),
		},
	})
}

// sytest: /upgrade of a bogus room fails gracefully
func TestUpgradeBogusRoomFails(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	resp := alice.DoFunc(
		t,
		"POST",
		[]string{"_matrix", "client", "v3", "rooms", "!fail:unknown", "upgrade"},
		client.WithJSONBody(t, map[string]string{
			"new_version": "6",
		}),
	)

	must.MatchResponse(t, resp, match.HTTPResponse{
		StatusCode: 404,
		JSON: []match.JSON{
			match.JSONKeyEqual("errcode", "M_NOT_FOUND"),
		},
	})
}

// sytest: /upgrade to an unknown version is rejected
func TestUpgradeRoomFailsUnknownVersion(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	resp := alice.DoFunc(
		t,
		"POST",
		[]string{"_matrix", "client", "v3", "rooms", "!fail:unknown", "upgrade"},
		client.WithJSONBody(t, map[string]string{
			"new_version": "jp.dragon-ball.v9000",
		}),
	)

	must.MatchResponse(t, resp, match.HTTPResponse{
		StatusCode: 400,
		JSON: []match.JSON{
			match.JSONKeyEqual("errcode", "M_UNSUPPORTED_ROOM_VERSION"),
		},
	})
}
