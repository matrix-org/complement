//go:build !dendrite_blacklist
// +build !dendrite_blacklist

// This file contains tests for a join rule which mixes concepts of restricted joins
// and knocking. This is implemented in room version 10.
//
// Generally, this is a combination of knocking_test and restricted_rooms_test.

package tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/helpers"
)

var (
	roomVersion = "10"
	joinRule    = "knock_restricted"
)

// See TestKnocking
func TestKnockingInMSC3787Room(t *testing.T) {
	doTestKnocking(t, roomVersion, joinRule)
}

// See TestKnockRoomsInPublicRoomsDirectory
func TestKnockRoomsInPublicRoomsDirectoryInMSC3787Room(t *testing.T) {
	doTestKnockRoomsInPublicRoomsDirectory(t, roomVersion, joinRule)
}

// See TestCannotSendKnockViaSendKnock
func TestCannotSendKnockViaSendKnockInMSC3787Room(t *testing.T) {
	testValidationForSendMembershipEndpoint(t, "/_matrix/federation/v1/send_knock", "knock",
		map[string]interface{}{
			"preset":       "public_chat",
			"room_version": roomVersion,
		},
	)
}

// See TestRestrictedRoomsLocalJoin
func TestRestrictedRoomsLocalJoinInMSC3787Room(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	// Setup the user, allowed room, and restricted room.
	alice, allowed_room, room := setupRestrictedRoom(t, deployment, roomVersion, joinRule)

	// Create a second user on the same homeserver.
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	// Execute the checks.
	checkRestrictedRoom(t, alice, bob, allowed_room, room, joinRule)
}

// See TestRestrictedRoomsRemoteJoin
func TestRestrictedRoomsRemoteJoinInMSC3787Room(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	// Setup the user, allowed room, and restricted room.
	alice, allowed_room, room := setupRestrictedRoom(t, deployment, roomVersion, joinRule)

	// Create a second user on a different homeserver.
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})

	// Execute the checks.
	checkRestrictedRoom(t, alice, bob, allowed_room, room, joinRule)
}

// See TestRestrictedRoomsRemoteJoinLocalUser
func TestRestrictedRoomsRemoteJoinLocalUserInMSC3787Room(t *testing.T) {
	doTestRestrictedRoomsRemoteJoinLocalUser(t, roomVersion, joinRule)
}

// See TestRestrictedRoomsRemoteJoinFailOver
func TestRestrictedRoomsRemoteJoinFailOverInMSC3787Room(t *testing.T) {
	doTestRestrictedRoomsRemoteJoinFailOver(t, roomVersion, joinRule)
}
