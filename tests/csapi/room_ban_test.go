package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

// This is technically a tad different from the sytest, in that it doesnt try to ban a @random_dude:test,
// but this will actually validate against a present user in the room.
// sytest: Non-present room members cannot ban others
func TestNotPresentUserCannotBanOthers(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	charlie := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	bob.MustJoinRoom(t, roomID, nil)

	alice.SendEventSynced(t, roomID, b.Event{
		Type:     "m.room.power_levels",
		StateKey: b.Ptr(""),
		Content: map[string]interface{}{
			"users": map[string]interface{}{
				charlie.UserID: 100,
			},
		},
	})

	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	res := charlie.Do(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "ban"}, client.WithJSONBody(t, map[string]interface{}{
		"user_id": bob.UserID,
		"reason":  "testing",
	}))

	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 403,
	})
}

func TestCanBanJoinedUser(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset":   "public_chat",
		"cryptoid": alice.NewCryptoID(t),
	}, client.WithCreateEndpointVersion(client.CreateRoomURLMSC4080))

	// Bob joins room
	bob.MustJoinRoom(t, roomID, []string{}, client.WithJoinEndpointVersion(client.JoinRoomURLMSC4080))
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	// Alice bans bob
	alice.MustBanFromRoom(t, roomID, bob.UserID, client.WithBanEndpointVersion(client.BanRoomURLMSC4080))
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(bob.UserID, roomID))
}

func TestBannedUserCannotRejoin(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset":   "public_chat",
		"cryptoid": alice.NewCryptoID(t),
	}, client.WithCreateEndpointVersion(client.CreateRoomURLMSC4080))

	// Bob joins room
	bob.MustJoinRoom(t, roomID, []string{}, client.WithJoinEndpointVersion(client.JoinRoomURLMSC4080))
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	// Alice bans bob
	alice.MustBanFromRoom(t, roomID, bob.UserID, client.WithBanEndpointVersion(client.BanRoomURLMSC4080))

	// Bob attempts to rejoin room
	res := bob.JoinRoom(t, roomID, []string{}, client.WithJoinEndpointVersion(client.JoinRoomURLMSC4080))
	must.MatchFailure(t, res)
}

func TestCanUnbanBannedUser(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset":   "public_chat",
		"cryptoid": alice.NewCryptoID(t),
	}, client.WithCreateEndpointVersion(client.CreateRoomURLMSC4080))

	// Bob joins room
	bob.MustJoinRoom(t, roomID, []string{}, client.WithJoinEndpointVersion(client.JoinRoomURLMSC4080))
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	// Alice bans bob
	alice.MustBanFromRoom(t, roomID, bob.UserID, client.WithBanEndpointVersion(client.BanRoomURLMSC4080))

	// Bob attempts to rejoin room
	res := bob.JoinRoom(t, roomID, []string{}, client.WithJoinEndpointVersion(client.JoinRoomURLMSC4080))
	must.MatchFailure(t, res)

	// Alice unbans bob
	alice.MustUnbanFromRoom(t, roomID, bob.UserID, client.WithUnbanEndpointVersion(client.UnbanRoomURLMSC4080))

	// Bob rejoins room
	bob.MustJoinRoom(t, roomID, []string{}, client.WithJoinEndpointVersion(client.JoinRoomURLMSC4080))
}
