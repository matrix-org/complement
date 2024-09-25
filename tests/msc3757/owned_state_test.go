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
const stateEventTestType = "com.example.test"

// To stress-test parsing, include separator & sigil characters
const stateKeySuffix = "_state_key_suffix:!@#$123"

func TestWithoutOwnedState(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	creator := deployment.Register(t, hsName, helpers.RegistrationOpts{})
	user := deployment.Register(t, hsName, helpers.RegistrationOpts{})

	roomID := creator.MustCreateRoom(t, map[string]interface{}{
		"room_version": "10",
		"preset":       "public_chat",
		"power_level_content_override": map[string]interface{}{
			"events": map[string]int{
				stateEventTestType: 0,
			},
		},
	})
	user.MustJoinRoom(t, roomID, nil)

	t.Run("parallel", func(t *testing.T) {
		t.Run("user can set state with their own user ID as state key", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, roomID, user, user.UserID)
			must.MatchSuccess(t, res)
		})
		t.Run("room creator cannot set state with their own suffixed user ID as state key", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, roomID, creator, creator.UserID+stateKeySuffix)
			mustMatchForbidden(t, res)
		})
		t.Run("room creator cannot set state with another user ID as state key", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, roomID, creator, user.UserID)
			mustMatchForbidden(t, res)
		})
		t.Run("room creator cannot set state with another suffixed user ID as state key", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, roomID, creator, user.UserID+stateKeySuffix)
			mustMatchForbidden(t, res)
		})
		t.Run("room creator cannot set state with a non-member user ID as state key", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, roomID, creator, "@notinroom:hs2")
			mustMatchForbidden(t, res)
		})
		t.Run("room creator cannot set state with malformed user ID as state key", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, roomID, creator, "@oops")
			mustMatchForbidden(t, res)
		})
	})
}

func TestMSC3757OwnedState(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	creator := deployment.Register(t, hsName, helpers.RegistrationOpts{})
	user1 := deployment.Register(t, hsName, helpers.RegistrationOpts{})
	user2 := deployment.Register(t, hsName, helpers.RegistrationOpts{})

	roomID := creator.MustCreateRoom(t, map[string]interface{}{
		"room_version": "org.matrix.msc3757.10",
		"preset":       "public_chat",
		"power_level_content_override": map[string]interface{}{
			"events": map[string]int{
				stateEventTestType: 0,
			},
		},
	})
	user1.MustJoinRoom(t, roomID, nil)
	user2.MustJoinRoom(t, roomID, nil)

	t.Run("parallel", func(t *testing.T) {
		t.Run("user can set state with their own suffixed user ID as state key", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, roomID, user1, user1.UserID+stateKeySuffix)
			must.MatchSuccess(t, res)
		})
		t.Run("room creator can set state with another user ID as state key", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, roomID, creator, user1.UserID)
			must.MatchSuccess(t, res)
		})
		t.Run("room creator can set state with another suffixed user ID as state key", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, roomID, creator, user1.UserID+stateKeySuffix)
			must.MatchSuccess(t, res)
		})
		t.Run("user cannot set state with another user ID as state key", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, roomID, user1, user2.UserID)
			mustMatchForbidden(t, res)
		})
		t.Run("user cannot set state with another suffixed user ID as state key", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, roomID, user1, user2.UserID+stateKeySuffix)
			mustMatchForbidden(t, res)
		})
		t.Run("user cannot set state with unseparated suffix in state key", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, roomID, user1, user1.UserID+stateKeySuffix[1:])
			mustMatchForbidden(t, res)
		})
		t.Run("user cannot set state with misplaced user ID in state key", func(t *testing.T) {
			t.Parallel()
			// Still put @ at start of state key, because without it, there is no write protection at all
			res := sendState(t, roomID, user1, "@prefix_"+user1.UserID+stateKeySuffix)
			mustMatchForbidden(t, res)
		})
		t.Run("room creator can set state with a non-member user ID as state key", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, roomID, creator, "@notinroom:hs2")
			must.MatchSuccess(t, res)
		})
		t.Run("room creator cannot set state with malformed user ID as state key", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, roomID, creator, "@oops")
			mustMatchInvalid(t, res)
		})
		t.Run("room creator cannot set state with improperly suffixed state key", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, roomID, creator, creator.UserID+"@"+stateKeySuffix[1:])
			mustMatchInvalid(t, res)
		})
	})
}

func sendState(t *testing.T, roomID string, user *client.CSAPI, stateKey string) *http.Response {
	t.Helper()
	return user.Do(
		t,
		"PUT",
		[]string{"_matrix", "client", "v3", "rooms", roomID, "state", stateEventTestType, stateKey},
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

func mustMatchInvalid(t *testing.T, res *http.Response) {
	t.Helper()
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 400,
		JSON: []match.JSON{
			match.JSONKeyEqual("errcode", "M_BAD_JSON"),
		},
	})
}
