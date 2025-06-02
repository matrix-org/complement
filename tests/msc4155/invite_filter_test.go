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
		roomID := mustCreateRoomAndSync(t, bob)
		bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
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
		roomID := mustCreateRoomAndSync(t, bob)
		mustInviteRoomAndFail(t, bob, roomID, alice.UserID)
		mustHaveNoInviteInSyncResponse(t, alice)
	})
	t.Run("Can ignore a single user", func(t *testing.T) {
		mustSetInviteConfig(t, alice, InviteFilterConfig{
			IgnoredUsers: []string{bob.UserID},
		})
		roomID := mustCreateRoomAndSync(t, bob)
		// Note, this invite failed invisibly.
		bob.MustInviteRoom(t, roomID, alice.UserID)
		mustHaveNoInviteInSyncResponse(t, alice)
	})
	t.Run("Can block a whole server", func(t *testing.T) {
		mustSetInviteConfig(t, alice, InviteFilterConfig{
			BlockedServers: []string{hs2Name},
		})
		roomIDA := mustCreateRoomAndSync(t, bob)
		mustInviteRoomAndFail(t, bob, roomIDA, alice.UserID)
		roomIDB := mustCreateRoomAndSync(t, evil_bob)
		mustInviteRoomAndFail(t, evil_bob, roomIDB, alice.UserID)
		mustHaveNoInviteInSyncResponse(t, alice)
	})
	t.Run("Can ignore a whole server", func(t *testing.T) {
		mustSetInviteConfig(t, alice, InviteFilterConfig{
			IgnoredServers: []string{hs2Name},
		})
		roomIDA := mustCreateRoomAndSync(t, bob)
		bob.MustInviteRoom(t, roomIDA, alice.UserID)
		roomIDB := mustCreateRoomAndSync(t, evil_bob)
		evil_bob.MustInviteRoom(t, roomIDB, alice.UserID)
		mustHaveNoInviteInSyncResponse(t, alice)
	})
	t.Run("Can glob serveral servers", func(t *testing.T) {
		mustSetInviteConfig(t, alice, InviteFilterConfig{
			BlockedServers: []string{"hs*"},
		})
		roomIDA := mustCreateRoomAndSync(t, bob)
		mustInviteRoomAndFail(t, bob, roomIDA, alice.UserID)
		roomIDB := mustCreateRoomAndSync(t, evil_bob)
		mustInviteRoomAndFail(t, evil_bob, roomIDB, alice.UserID)
		roomIDC := evil_alice.MustCreateRoom(t, map[string]interface{}{
			"preset": "private_chat",
		})
		mustInviteRoomAndFail(t, evil_alice, roomIDC, alice.UserID)

		mustHaveNoInviteInSyncResponse(t, alice)
	})
	t.Run("Can allow a user from a blocked server", func(t *testing.T) {
		mustSetInviteConfig(t, alice, InviteFilterConfig{
			AllowedUsers: []string{bob.UserID},
			BlockedServers: []string{hs2Name},
		})

		roomIDBlocked := mustCreateRoomAndSync(t, evil_bob)
		mustInviteRoomAndFail(t, evil_bob, roomIDBlocked, alice.UserID)
		mustHaveNoInviteInSyncResponse(t, alice)
		roomIDAllowed := mustCreateRoomAndSync(t, bob)
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

		roomIDBlocked := mustCreateRoomAndSync(t, evil_bob)
		mustInviteRoomAndFail(t, evil_bob, roomIDBlocked, alice.UserID)
		mustHaveNoInviteInSyncResponse(t, alice)
		roomIDAllowed := mustCreateRoomAndSync(t, bob)
		bob.MustInviteRoom(t, roomIDAllowed, alice.UserID)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, roomIDAllowed))
		alice.MustJoinRoom(t, roomIDAllowed, []spec.ServerName{})
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomIDAllowed))
		bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomIDAllowed))
	})
}

// Tests that a given invite filter config is properly set
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

// Tests that a room is created and appears down the creators sync 
func mustCreateRoomAndSync(t *testing.T, c *client.CSAPI) string {
	t.Helper()
	roomID := c.MustCreateRoom(t, map[string]interface{}{
		"preset": "private_chat",
	})
	c.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(c.UserID, roomID))
	return roomID
}

// Test that requests to invite a given user fail with a 403 response
func mustInviteRoomAndFail(t *testing.T, c *client.CSAPI,roomID string, userID string) {
	t.Helper()
	res := c.InviteRoom(t, roomID, userID)
	if res.StatusCode == 403 {
		return
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	ct.Fatalf(t, "CSAPI.Must: %s %s returned non-403 code: %s - body: %s", res.Request.Method, res.Request.URL.String(), res.Status, string(body))
}

// Test that no invites appear down sync
func mustHaveNoInviteInSyncResponse(t *testing.T, c *client.CSAPI) {
	initialSyncResponse, _ := c.MustSync(t, client.SyncReq{})
	must.MatchGJSON(
		t,
		initialSyncResponse,
		match.JSONKeyMissing("rooms.invite"),
	)
}
