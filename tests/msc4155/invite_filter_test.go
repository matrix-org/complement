package tests

import (
	"testing"
	"log"
	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/matrix-org/complement/client"
	"encoding/json"
	"github.com/matrix-org/complement/ct"
	"io"
)

const hs1Name = "hs1"
const hs2Name = "hs2"
const inviteFilterAccountData = "org.matrix.msc4155.invite_permission_config"

// TODO: Test pagination of `GET /_matrix/client/v1/delayed_events` once
// it is implemented in a homeserver.

// As described in https://github.com/Johennes/matrix-spec-proposals/blob/johannes/invite-filtering/proposals/4155-invite-filtering.md#proposal
type InviteFilterConfig struct {
	AllowedUsers []string `json:"allowed_users,omitempty"`
	IgnoredUsers []string `json:"ignored_users,omitempty"`
	BlockedUsers []string `json:"blocked_users,omitempty"`
	AllowedServers []string `json:"allowed_servers,omitempty"`
	IgnoredServers []string `json:"ignored_servers,omitempty"`
	BlockedServers []string `json:"blocked_servers,omitempty"`
}

func TestInviteFiltering(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	// Invitee
	alice := deployment.Register(t, hs1Name, helpers.RegistrationOpts{})
	evil_alice := deployment.Register(t, hs1Name, helpers.RegistrationOpts{})
	
	bob := deployment.Register(t, hs2Name, helpers.RegistrationOpts{})
	evil_bob := deployment.Register(t, hs2Name, helpers.RegistrationOpts{})

	t.Run("Can invite users normally without any rules", func(t *testing.T) {
		mustSetInviteConfig(t, alice, InviteFilterConfig{})
		roomID := bob.MustCreateRoom(t, map[string]interface{}{
			"preset": "private_chat",
		})
		bob.MustInviteRoom(t, roomID, alice.UserID)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, roomID))
		alice.MustJoinRoom(t, roomID, []spec.ServerName{})
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
		bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
	})
	t.Run("Can block a single user", func(t *testing.T) {
		mustSetInviteConfig(t, alice, InviteFilterConfig{
			BlockedUsers: []string{bob.UserID},
		})
		roomID := bob.MustCreateRoom(t, map[string]interface{}{
			"preset": "private_chat",
		})
		MustInviteRoomAndFail(t, bob, roomID, alice.UserID)
		MustHaveNoInviteInSyncResponse(t, alice)
	})
	t.Run("Can ignore a single user", func(t *testing.T) {
		mustSetInviteConfig(t, alice, InviteFilterConfig{
			IgnoredUsers: []string{bob.UserID},
		})
		roomID := bob.MustCreateRoom(t, map[string]interface{}{
			"preset": "private_chat",
		})
		// Note, this invite failed invisibly.
		bob.MustInviteRoom(t, roomID, alice.UserID)
		MustHaveNoInviteInSyncResponse(t, alice)
	})
	t.Run("Can block a whole server", func(t *testing.T) {
		mustSetInviteConfig(t, alice, InviteFilterConfig{
			BlockedServers: []string{hs2Name},
		})
		roomIDA := bob.MustCreateRoom(t, map[string]interface{}{
			"preset": "private_chat",
		})
		MustInviteRoomAndFail(t, bob, roomIDA, alice.UserID)
		roomIDB := evil_bob.MustCreateRoom(t, map[string]interface{}{
			"preset": "private_chat",
		})
		MustInviteRoomAndFail(t, evil_bob, roomIDB, alice.UserID)
		MustHaveNoInviteInSyncResponse(t, alice)
	})
	t.Run("Can ignore a whole server", func(t *testing.T) {
		mustSetInviteConfig(t, alice, InviteFilterConfig{
			IgnoredServers: []string{hs2Name},
		})
		roomIDA := bob.MustCreateRoom(t, map[string]interface{}{
			"preset": "private_chat",
		})
		bob.MustInviteRoom(t, roomIDA, alice.UserID)
		roomIDB := evil_bob.MustCreateRoom(t, map[string]interface{}{
			"preset": "private_chat",
		})
		evil_bob.MustInviteRoom(t, roomIDB, alice.UserID)
		MustHaveNoInviteInSyncResponse(t, alice)
	})
	t.Run("Can glob serveral servers", func(t *testing.T) {
		mustSetInviteConfig(t, alice, InviteFilterConfig{
			BlockedServers: []string{"hs*"},
		})

		roomIDA := bob.MustCreateRoom(t, map[string]interface{}{
			"preset": "private_chat",
		})
		MustInviteRoomAndFail(t, bob, roomIDA, alice.UserID)
		roomIDB := evil_bob.MustCreateRoom(t, map[string]interface{}{
			"preset": "private_chat",
		})
		MustInviteRoomAndFail(t, evil_bob, roomIDB, alice.UserID)
		roomIDC := evil_alice.MustCreateRoom(t, map[string]interface{}{
			"preset": "private_chat",
		})
		MustInviteRoomAndFail(t, evil_alice, roomIDC, alice.UserID)

		MustHaveNoInviteInSyncResponse(t, alice)
	})
	t.Run("Can allow a user from a blocked server", func(t *testing.T) {
		mustSetInviteConfig(t, alice, InviteFilterConfig{
			AllowedUsers: []string{bob.UserID},
			BlockedServers: []string{hs2Name},
		})

		roomIDBlocked := evil_bob.MustCreateRoom(t, map[string]interface{}{
			"preset": "private_chat",
		})
		MustInviteRoomAndFail(t, evil_bob, roomIDBlocked, alice.UserID)
		MustHaveNoInviteInSyncResponse(t, alice)
		roomIDAllowed := bob.MustCreateRoom(t, map[string]interface{}{
			"preset": "private_chat",
		})
		bob.MustInviteRoom(t, roomIDAllowed, alice.UserID)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, roomIDAllowed))
		alice.MustJoinRoom(t, roomIDAllowed, []spec.ServerName{})
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomIDAllowed))
		bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomIDAllowed))
	})
	t.Run("Can block a user from an allowed server", func(t *testing.T) {
		mustSetInviteConfig(t, alice, InviteFilterConfig{
			BlockedUsers: []string{evil_bob.UserID},
			AllowedServers: []string{hs2Name},
		})

		roomIDBlocked := evil_bob.MustCreateRoom(t, map[string]interface{}{
			"preset": "private_chat",
		})
		MustInviteRoomAndFail(t, evil_bob, roomIDBlocked, alice.UserID)
		MustHaveNoInviteInSyncResponse(t, alice)
		roomIDAllowed := bob.MustCreateRoom(t, map[string]interface{}{
			"preset": "private_chat",
		})
		bob.MustInviteRoom(t, roomIDAllowed, alice.UserID)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, roomIDAllowed))
		alice.MustJoinRoom(t, roomIDAllowed, []spec.ServerName{})
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomIDAllowed))
		bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomIDAllowed))
	})
}

func mustSetInviteConfig(t *testing.T, c *client.CSAPI, cfg InviteFilterConfig) {
	log.Printf("Setting invite config A %+v\n", cfg)

	b, err := json.Marshal(&cfg)
	if err != nil {
		panic(err)
	}
	var m map[string]interface{}
	err = json.Unmarshal(b, &m)
	if err != nil {
		panic(err)
	}
	log.Printf("Setting invite config B %+v\n", m)
	c.MustSetGlobalAccountData(t, inviteFilterAccountData, m)
}

// InviteRoom invites userID to the room ID, else fails the test.
func MustInviteRoomAndFail(t *testing.T, c *client.CSAPI,roomID string, userID string) {
	t.Helper()
	res := c.InviteRoom(t, roomID, userID)
	if res.StatusCode == 403 {
		return
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	ct.Fatalf(t, "CSAPI.Must: %s %s returned non-403 code: %s - body: %s", res.Request.Method, res.Request.URL.String(), res.Status, string(body))
}

// InviteRoom invites userID to the room ID, else fails the test.
func MustHaveNoInviteInSyncResponse(t *testing.T, c *client.CSAPI) {
	initialSyncResponse, _ := c.MustSync(t, client.SyncReq{})
	must.MatchGJSON(
		t,
		initialSyncResponse,
		match.JSONKeyMissing("rooms.invite"),
	)
}
