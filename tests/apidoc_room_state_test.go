package tests

import (
	"crypto/rand"
	"io/ioutil"
	"math/big"
	"net/url"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestRoomState(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	authedClient := deployment.Client(t, "hs1", "@alice:hs1")
	t.Run("Parallel", func(t *testing.T) {
		// sytest: GET /rooms/:room_id/state/m.room.member/:user_id fetches my membership
		t.Run("GET /rooms/:room_id/state/m.room.member/:user_id fetches my membership", func(t *testing.T) {
			t.Parallel()
			roomID, _ := roomFixture(t, authedClient)
			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "state", "m.room.member", authedClient.UserID})
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
			roomID, _ := roomFixture(t, authedClient)
			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "state", "m.room.member", authedClient.UserID}, client.WithQueries(queryParams))
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
			roomID, _ := roomFixture(t, authedClient)
			res := authedClient.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomID, "state", "m.room.power_levels"})
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
	})
}

func roomFixture(t *testing.T, authedClient *client.CSAPI) (roomID string, roomAlias string) {
	t.Helper()
	roomAlias, err := generateRoomAlias()
	if err != nil {
		t.Fatalf("unable to generate room alias: %v", err)
	}
	reqBody := client.WithJSONBody(t, map[string]interface{}{
		"visibility":      "public",
		"preset":          "public_chat",
		"room_alias_name": roomAlias,
	})

	res := authedClient.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "createRoom"}, reqBody)

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("unable to read response body: %v", err)
	}

	roomID = gjson.GetBytes(body, "room_id").Str
	roomAlias = gjson.GetBytes(body, "room_alias").Str
	return roomID, roomAlias
}

func generateRoomAlias() (string, error) {
	const letters = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-"
	ret := make([]byte, 32)
	for i := 0; i < 32; i++ {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			return "", err
		}
		ret[i] = letters[num.Int64()]
	}

	return string(ret), nil
}
