package tests

import (
	"net/http"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

const hsName = "hs1"
const eventTestType = "dev.zirco.test"

func TestWithoutStatePLs(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	creator := deployment.Register(t, hsName, helpers.RegistrationOpts{})
	user := deployment.Register(t, hsName, helpers.RegistrationOpts{})

	roomID := creator.MustCreateRoom(t, map[string]interface{}{
		"room_version": "12",
		"preset":       "public_chat",
		"power_level_content_override": map[string]interface{}{
			"events": map[string]int{
				eventTestType: 0,
			},
			"events_default": 1,
		},
	})
	user.MustJoinRoom(t, roomID, nil)

	t.Run("user can send state event based on events PL", func(t *testing.T) {
		res := sendState(t, roomID, user)
		must.MatchSuccess(t, res)
	})
}

func TestWithStatePLs(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	creator := deployment.Register(t, hsName, helpers.RegistrationOpts{})
	user := deployment.Register(t, hsName, helpers.RegistrationOpts{})

	roomID := creator.MustCreateRoom(t, map[string]interface{}{
		"room_version": "12",
		"preset":       "public_chat",
		"power_level_content_override": map[string]interface{}{
			"events": map[string]int{
				eventTestType: 0,
			},
			"events_default": 1,
		},
	})
	user.MustJoinRoom(t, roomID, nil)

	t.Run("parallel", func(t *testing.T) {
		t.Run("user can send message event based on events PL", func(t *testing.T) {
			t.Parallel()
			res := sendMessage(t, roomID, user)
			must.MatchSuccess(t, res)
		})

		t.Run("user cannot send state event based on events PL", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, roomID, user)
			mustMatchForbidden(t, res)
		})

		t.Run("creator can still send state event", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, roomID, creator)
			must.MatchSuccess(t, res)
		})
	})
}

func sendMessage(t *testing.T, roomID string, user *client.CSAPI) *http.Response {
	t.Helper()
	return user.Do(
		t,
		"PUT",
		[]string{"_matrix", "client", "v3", "rooms", roomID, "send", eventTestType, "txn1"},
		client.WithJSONBody(t, map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world",
		}),
	)
}

func sendState(t *testing.T, roomID string, user *client.CSAPI) *http.Response {
	t.Helper()
	return user.Do(
		t,
		"PUT",
		[]string{"_matrix", "client", "v3", "rooms", roomID, "state", eventTestType, ""},
		client.WithJSONBody(t, map[string]interface{}{}),
	)
}

func mustMatchForbidden(t *testing.T, res *http.Response) {
	t.Helper()
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 403,
		JSON: []match.JSON{
			match.JSONKeyEqual("errcode", "M_FORBIDDEN"),
		},
	})
}
