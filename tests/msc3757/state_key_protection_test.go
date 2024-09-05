package tests

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

const stateEventType = "org.matrix.msc3757.test"

func TestStateKeyProtection(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	admin := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	user1 := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	user2 := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := admin.MustCreateRoom(t, map[string]interface{}{
		"room_version": "org.matrix.msc3757.10",
		"preset":       "public_chat",
		"power_level_content_override": map[string]interface{}{
			"events": map[string]int{
				stateEventType: 0,
			},
		},
	})
	user1.MustJoinRoom(t, roomID, nil)
	user2.MustJoinRoom(t, roomID, nil)

	sendState := func(t *testing.T, user *client.CSAPI, stateKey string, body string) *http.Response {
		t.Helper()
		return user.Do(
			t,
			"PUT",
			[]string{"_matrix", "client", "v3", "rooms", roomID, "state", stateEventType, stateKey},
			client.WithJSONBody(t, map[string]interface{}{
				"body": body,
			}),
		)
	}

	testProtectedStateKey := func(t *testing.T, getStateKeyForUser func(*client.CSAPI) string) {
		t.Helper()
		t.Run("user can set state with a key under their control", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, user1, getStateKeyForUser(user1), "own state")
			must.MatchSuccess(t, res)
		})
		t.Run("admin can set state with a key under a non-admin's control", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, admin, getStateKeyForUser(admin), "admin's state")
			must.MatchSuccess(t, res)
		})
		t.Run("user cannot set state with a key under another user's control", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, user1, getStateKeyForUser(user2), "another's state")
			mustMatchForbidden(t, res)
		})
	}

	t.Run("parallel", func(t *testing.T) {
		t.Run("with state key as user ID", func(t *testing.T) {
			t.Parallel()
			testProtectedStateKey(t, func(user *client.CSAPI) string {
				return user.UserID
			})
		})
		t.Run("with state key as user ID + device ID", func(t *testing.T) {
			t.Parallel()
			testProtectedStateKey(t, func(user *client.CSAPI) string {
				return fmt.Sprintf("%s_%s", user.UserID, user.DeviceID)
			})
		})
		t.Run("with state key as user ID + multi-underscore string", func(t *testing.T) {
			t.Parallel()
			testProtectedStateKey(t, func(user *client.CSAPI) string {
				return fmt.Sprintf("%s_multi_underscore_suffix", user.UserID)
			})
		})
		t.Run("with state key as @-prefixed string that is not a user ID", func(t *testing.T) {
			t.Parallel()
			res := sendState(t, admin, "@oops", "")
			mustMatchInvalid(t, res)
		})
	})
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
