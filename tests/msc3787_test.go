//go:build msc3787
// +build msc3787

// This file contains tests for a join rule which mixes concepts of restricted joins
// and knocking. This is currently experimental and defined by MSC3787, found here:
// https://github.com/matrix-org/matrix-spec-proposals/pull/3787
//
// Generally, this is a combination of knocking_test and restricted_rooms_test.

package tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
)

var (
	msc3787RoomVersion = "org.matrix.msc3787"
	msc3787JoinRule    = "knock_restricted"
)

// See TestKnocking
func TestKnockingInMSC3787Room(t *testing.T) {
	doTestKnocking(t, msc3787RoomVersion, msc3787JoinRule)
}

// See TestKnockRoomsInPublicRoomsDirectory
func TestKnockRoomsInPublicRoomsDirectoryInMSC3787Room(t *testing.T) {
	doTestKnockRoomsInPublicRoomsDirectory(t, msc3787RoomVersion, msc3787JoinRule)
}

// See TestCannotSendKnockViaSendKnock
func TestCannotSendKnockViaSendKnockInMSC3787Room(t *testing.T) {
	testValidationForSendMembershipEndpoint(t, "/_matrix/federation/v1/send_knock", "knock",
		map[string]interface{}{
			"preset":       "public_chat",
			"room_version": msc3787RoomVersion,
		},
	)
}

// See TestRestrictedRoomsLocalJoin
func TestRestrictedRoomsLocalJoinInMSC3787Room(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	// Setup the user, allowed room, and restricted room.
	alice, allowed_room, room := setupRestrictedRoom(t, deployment, msc3787RoomVersion, msc3787JoinRule)

	// Create a second user on the same homeserver.
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	// Execute the checks.
	checkRestrictedRoom(t, alice, bob, allowed_room, room, msc3787JoinRule)
}

// See TestRestrictedRoomsRemoteJoin
func TestRestrictedRoomsRemoteJoinInMSC3787Room(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationOneToOneRoom)
	defer deployment.Destroy(t)

	// Setup the user, allowed room, and restricted room.
	alice, allowed_room, room := setupRestrictedRoom(t, deployment, msc3787RoomVersion, msc3787JoinRule)

	// Create a second user on a different homeserver.
	bob := deployment.Client(t, "hs2", "@bob:hs2")

	// Execute the checks.
	checkRestrictedRoom(t, alice, bob, allowed_room, room, msc3787JoinRule)
}

// See TestRestrictedRoomsRemoteJoinLocalUser
func TestRestrictedRoomsRemoteJoinLocalUserInMSC3787Room(t *testing.T) {
	doTestRestrictedRoomsRemoteJoinLocalUser(t, msc3787RoomVersion, msc3787JoinRule)
}

// See TestRestrictedRoomsRemoteJoinFailOver
func TestRestrictedRoomsRemoteJoinFailOverInMSC3787Room(t *testing.T) {
	doTestRestrictedRoomsRemoteJoinFailOver(t, msc3787RoomVersion, msc3787JoinRule)
}
