package csapi_tests

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"

	"net/http"
)

func TestRoomState(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	authedClient := deployment.Client(t, "hs1", "@alice:hs1")
	t.Run("Parallel", func(t *testing.T) {
		// sytest: GET /rooms/:room_id/state/m.room.member/:user_id fetches my membership
		t.Run("GET /rooms/:room_id/state/m.room.member/:user_id fetches my membership", func(t *testing.T) {
			t.Parallel()
			roomID := authedClient.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
			})
			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.member", authedClient.UserID})
			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("membership"),
					match.JSONKeyTypeEqual("membership", gjson.String),
					match.JSONKeyEqual("membership", "join"),
				},
			})
		})
		// sytest: GET /rooms/:room_id/state/m.room.member/:user_id?format=event fetches my membership event
		t.Run("GET /rooms/:room_id/state/m.room.member/:user_id?format=event fetches my membership event", func(t *testing.T) {
			t.Parallel()
			queryParams := url.Values{}
			queryParams.Set("format", "event")
			roomID := authedClient.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
			})
			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.member", authedClient.UserID}, client.WithQueries(queryParams))
			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("sender"),
					match.JSONKeyPresent("room_id"),
					match.JSONKeyPresent("content"),
					match.JSONKeyEqual("sender", authedClient.UserID),
					match.JSONKeyEqual("room_id", roomID),
					match.JSONKeyEqual("content.membership", "join"),
				},
			})
		})
		// sytest: GET /rooms/:room_id/state/m.room.power_levels fetches powerlevels
		t.Run("GET /rooms/:room_id/state/m.room.power_levels fetches powerlevels", func(t *testing.T) {
			t.Parallel()

			roomID := authedClient.CreateRoom(t, map[string]interface{}{
				"visibility":      "public",
				"preset":          "public_chat",
				"room_alias_name": "room_alias",
			})
			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.power_levels"})

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("ban"),
					match.JSONKeyPresent("kick"),
					match.JSONKeyPresent("redact"),
					match.JSONKeyPresent("users_default"),
					match.JSONKeyPresent("state_default"),
					match.JSONKeyPresent("events_default"),
					match.JSONKeyPresent("users"),
					match.JSONKeyPresent("events"),
				},
			})
		})
		// sytest: GET /rooms/:room_id/joined_members fetches my membership
		t.Run("GET /rooms/:room_id/joined_members fetches my membership", func(t *testing.T) {
			t.Parallel()
			roomID := authedClient.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
			})

			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "joined_members"})
			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("joined"),
					match.JSONKeyPresent("joined." + authedClient.UserID),
					match.JSONKeyPresent("joined." + authedClient.UserID + ".display_name"),
					match.JSONKeyPresent("joined." + authedClient.UserID + ".avatar_url"),
				},
			})
		})
		// sytest: GET /publicRooms lists newly-created room
		t.Run("GET /publicRooms lists newly-created room", func(t *testing.T) {
			t.Parallel()
			roomID := authedClient.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
			})

			authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "publicRooms"},
				client.WithRetryUntil(time.Second, func(res *http.Response) bool {
					foundRoom := false

					must.MatchResponse(t, res, match.HTTPResponse{
						JSON: []match.JSON{
							match.JSONKeyPresent("chunk"),
							match.JSONArrayEach("chunk", func(val gjson.Result) error {
								gotRoomID := val.Get("room_id").Str
								if gotRoomID == roomID {
									foundRoom = true
									return nil
								}
								return nil
							}),
						},
					})

					if !foundRoom {
						t.Logf("failed to find room with id: %s", roomID)
					}

					return foundRoom
				}),
			)
		})
		// sytest: GET /directory/room/:room_alias yields room ID
		t.Run("GET /directory/room/:room_alias yields room ID", func(t *testing.T) {
			t.Parallel()

			roomID := authedClient.CreateRoom(t, map[string]interface{}{
				"visibility":      "public",
				"preset":          "public_chat",
				"room_alias_name": "room_new",
			})

			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "directory", "room", "#room_new:hs1"})

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("room_id"),
					match.JSONKeyPresent("servers"),
					match.JSONKeyTypeEqual("room_id", gjson.String),
					match.JSONKeyEqual("room_id", roomID),
				},
			})
		})
		// sytest: GET /joined_rooms lists newly-created room
		t.Run("GET /joined_rooms lists newly-created room", func(t *testing.T) {
			t.Parallel()

			roomID := authedClient.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
			})
			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "joined_rooms"})

			foundRoom := false

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("joined_rooms"),
					match.JSONArrayEach("joined_rooms", func(val gjson.Result) error {
						gotRoomID := val.Str
						if gotRoomID == roomID {
							foundRoom = true
							return nil
						}
						return nil
					}),
				},
			})

			if !foundRoom {
				t.Errorf("failed to find room with id: %s", roomID)
			}
		})
		// sytest: GET /rooms/:room_id/state/m.room.name gets name
		t.Run("GET /rooms/:room_id/state/m.room.name gets name", func(t *testing.T) {
			t.Parallel()

			roomID := authedClient.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
				"name":       "room_name_test",
			})

			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.name"})

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("name"),
					match.JSONKeyEqual("name", "room_name_test"),
				},
			})
		})
		// sytest: POST /rooms/:room_id/state/m.room.name sets name
		t.Run("POST /rooms/:room_id/state/m.room.name sets name", func(t *testing.T) {
			t.Parallel()

			roomID := authedClient.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
			})

			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"name": "room_test_name",
			})

			_ = authedClient.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.name"}, reqBody)

			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.name"})

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("name"),
					match.JSONKeyEqual("name", "room_test_name"),
				},
			})
		})
		// sytest: GET /rooms/:room_id/state/m.room.topic gets topic
		t.Run("GET /rooms/:room_id/state/m.room.topic gets topic", func(t *testing.T) {
			t.Parallel()

			roomID := authedClient.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
				"topic":      "room_topic_test",
			})

			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.topic"})

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("topic"),
					match.JSONKeyEqual("topic", "room_topic_test"),
				},
			})
		})
		// sytest: POST /rooms/:room_id/state/m.room.topic sets topic
		t.Run("PUT /rooms/:room_id/state/m.room.topic sets topic", func(t *testing.T) {
			t.Parallel()

			roomID := authedClient.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
			})

			reqBody := client.WithJSONBody(t, map[string]interface{}{
				"topic": "room_test_topic",
			})

			_ = authedClient.MustDoFunc(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.topic"}, reqBody)

			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.topic"})

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("topic"),
					match.JSONKeyEqual("topic", "room_test_topic"),
				},
			})
		})
		// sytest: GET /rooms/:room_id/state fetches entire room state
		t.Run("GET /rooms/:room_id/state fetches entire room state", func(t *testing.T) {
			t.Parallel()

			roomID := authedClient.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
				"name":       "room_test",
				"topic":      "room_topic_test",
			})

			wantKeys := map[string]bool{
				"m.room.create":       true,
				"m.room.join_rules":   true,
				"m.room.name":         true,
				"m.room.power_levels": true,
				"m.room.topic":        true,
			}

			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state"})

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONArrayEach("", func(val gjson.Result) error {
						gotKey := val.Get("type").Str
						if wantKeys[gotKey] {
							delete(wantKeys, gotKey)
							return nil
						}
						return nil
					}),
				},
			})
			if len(wantKeys) != 0 {
				t.Errorf("/state did not return the following expected keys: %v", wantKeys)
			}
		})
		// sytest: POST /createRoom with creation content
		t.Run("PUT /createRoom with creation content", func(t *testing.T) {
			t.Parallel()

			roomID := authedClient.CreateRoom(t, map[string]interface{}{
				"visibility": "public",
				"preset":     "public_chat",
				"creation_content": map[string]interface{}{
					"m.federate": false,
				},
			})

			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.create"})

			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyPresent("m\\.federate"),
					match.JSONKeyEqual("m\\.federate", false),
				},
			})
		})
		t.Run("GET /rooms/:room_id/joined_members is forbidden after leaving room", func(t *testing.T) {
			t.Parallel()
			roomID := authedClient.CreateRoom(t, map[string]interface{}{})
			authedClient.LeaveRoom(t, roomID)
			res := authedClient.DoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "joined_members"})
			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: http.StatusForbidden,
				JSON: []match.JSON{
					match.JSONKeyEqual("errcode", "M_FORBIDDEN"),
				},
			})
		})
	})
}
