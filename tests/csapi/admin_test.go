package csapi_tests

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/gomatrixserverlib/spec"
)

// Check if this homeserver supports Synapse-style admin registration.
// Not all images support this currently.
func TestCanRegisterAdmin(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	deployment.Register(t, "hs1", helpers.RegistrationOpts{
		IsAdmin: true,
	})
}

// Test if the implemented /_synapse/admin/v1/send_server_notice behaves as expected
func TestServerNotices(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	admin := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		IsAdmin: true,
	})
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	reqBody := client.WithJSONBody(t, map[string]interface{}{
		"user_id": alice.UserID,
		"content": map[string]interface{}{
			"msgtype": "m.text",
			"body":    "hello from server notices!",
		},
	})
	var (
		eventID string
		roomID  string
	)
	t.Run("/send_server_notice is not allowed as normal user", func(t *testing.T) {
		res := alice.Do(t, "POST", []string{"_synapse", "admin", "v1", "send_server_notice"})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: http.StatusForbidden,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_FORBIDDEN"),
			},
		})
	})
	t.Run("/send_server_notice as an admin is allowed", func(t *testing.T) {
		eventID = sendServerNotice(t, admin, reqBody, nil)
	})
	t.Run("Alice is invited to the server alert room", func(t *testing.T) {
		roomID = syncUntilInvite(t, alice)
	})
	t.Run("Alice cannot reject the invite", func(t *testing.T) {
		res := alice.LeaveRoom(t, roomID)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: http.StatusForbidden,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_CANNOT_LEAVE_SERVER_NOTICE_ROOM"),
			},
		})
	})
	t.Run("Alice can join the alert room", func(t *testing.T) {
		alice.MustJoinRoom(t, roomID, []spec.ServerName{})
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, eventID))
	})
	t.Run("Alice can leave the alert room, after joining it", func(t *testing.T) {
		alice.MustLeaveRoom(t, roomID)
	})
	t.Run("After leaving the alert room and on re-invitation, no new room is created", func(t *testing.T) {
		sendServerNotice(t, admin, reqBody, nil)
		newRoomID := syncUntilInvite(t, alice)
		if roomID != newRoomID {
			t.Errorf("expected no new room but got one: %s != %s", roomID, newRoomID)
		}
	})
	t.Run("Sending a notice with a transactionID is idempotent", func(t *testing.T) {
		txnID := "1"
		eventID1 := sendServerNotice(t, admin, reqBody, &txnID)
		eventID2 := sendServerNotice(t, admin, reqBody, &txnID)
		if eventID1 != eventID2 {
			t.Errorf("expected event IDs to be the same, but got '%s' and '%s'", eventID1, eventID2)
		}
	})

}

func sendServerNotice(t *testing.T, admin *client.CSAPI, reqBody client.RequestOpt, txnID *string) (eventID string) {
	var res *http.Response
	if txnID != nil {
		res = admin.MustDo(t, "PUT", []string{"_synapse", "admin", "v1", "send_server_notice", *txnID}, reqBody)
	} else {
		res = admin.MustDo(t, "POST", []string{"_synapse", "admin", "v1", "send_server_notice"}, reqBody)
	}
	body := must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONKeyPresent("event_id"),
		},
	})
	return gjson.GetBytes(body, "event_id").Str
}

// syncUntilInvite checks if we got an invitation from the server notice sender, as the roomID is unknown.
// Returns the found roomID on success
func syncUntilInvite(t *testing.T, alice *client.CSAPI) string {
	var roomID string
	alice.MustSyncUntil(t, client.SyncReq{}, func(userID string, res gjson.Result) error {
		if res.Get("rooms.invite.*.invite_state.events.0.sender").Str == "@_server:hs1" {
			roomID = res.Get("rooms.invite").Get("@keys.0").Str
			return nil
		}
		return fmt.Errorf("invite not found")
	})
	return roomID
}
