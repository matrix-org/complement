//go:build msc3391
// +build msc3391

// This file contains tests for deleting account data as
// defined by MSC3391, which you can read here:
// https://github.com/matrix-org/matrix-doc/pull/3391

package tests

import (
	"fmt"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"

	"github.com/tidwall/gjson"
)

const testAccountDataType = "org.example.test"

var testAccountDataContent = map[string]interface{}{"test_data": 1}

func TestRemovingAccountData(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	// Create a user to manipulate the account data of
	aliceUserID := "@alice:hs1"
	alice := deployment.Client(t, "hs1", aliceUserID)

	// And create a room with that user where we can store some room account data
	roomID := alice.CreateRoom(t, map[string]interface{}{})

	// Test deleting global account data.
	t.Run("Deleting a user's account data via DELETE works", func(t *testing.T) {
		createUserAccountData(t, alice)
		deleteUserAccountData(t, alice, true)
	})
	t.Run("Deleting a user's account data via PUT works", func(t *testing.T) {
		createUserAccountData(t, alice)
		deleteUserAccountData(t, alice, false)
	})

	// Test deleting room account data.
	t.Run("Deleting a user's room data via DELETE works", func(t *testing.T) {
		createRoomAccountData(t, alice, roomID)
		deleteRoomAccountData(t, alice, true, roomID)
	})
	t.Run("Deleting a user's room account data via PUT works", func(t *testing.T) {
		createRoomAccountData(t, alice, roomID)
		deleteRoomAccountData(t, alice, false, roomID)
	})
}

// createUserAccountData creates some account data for a user and checks that it was
// created successfully by both querying the data afterwards, and ensuring it appears down /sync.
func createUserAccountData(t *testing.T, c *client.CSAPI) {
	// Retrieve a sync token for this user
	_, nextBatchToken := c.MustSync(
		t,
		client.SyncReq{},
	)

	// Set and check the account data
	// Create user account data
	c.SetGlobalAccountData(t, testAccountDataType, testAccountDataContent)

	// Wait for the account data to appear down /sync
	c.MustSyncUntil(
		t,
		client.SyncReq{
			Since: nextBatchToken,
		},
		client.SyncGlobalAccountDataHas(checkAccountDataContent),
	)

	// Also check the account data content by querying the appropriate endpoint
	res := c.GetGlobalAccountData(t, testAccountDataType)
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			func(body []byte) error {
				if !match.JSONDeepEqual(body, testAccountDataContent) {
					return fmt.Errorf(
						"Expected %s for room account data content, got '%s'",
						testAccountDataType,
						string(body),
					)
				}

				return nil
			},
		},
	})
}

// createRoomAccountData creates some account data for a room and checks that it was
// created successfully by both querying the data afterwards, and ensuring it appears down /sync.
func createRoomAccountData(t *testing.T, c *client.CSAPI, roomID string) {
	// Retrieve a sync token for this user
	_, nextBatchToken := c.MustSync(
		t,
		client.SyncReq{},
	)

	// Create room account data
	c.SetRoomAccountData(t, roomID, testAccountDataType, testAccountDataContent)

	// Wait for the account data to appear down /sync
	c.MustSyncUntil(
		t,
		client.SyncReq{
			Since: nextBatchToken,
		},
		client.SyncRoomAccountDataHas(roomID, checkAccountDataContent),
	)

	// Also check the account data content by querying the appropriate endpoint
	res := c.GetRoomAccountData(t, roomID, testAccountDataType)
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			func(body []byte) error {
				if !match.JSONDeepEqual(body, testAccountDataContent) {
					return fmt.Errorf(
						"Expected %s for room account data content, got '%s'",
						testAccountDataType,
						string(body),
					)
				}

				return nil
			},
		},
	})
}

// deleteUserAccountData removes account data for a user.
//
// If viaDelete is true, a request is made to the DELETE endpoint for user
// account data. Otherwise, the PUT method is used with an empty content
// dictionary instead. MSC3391 specifies that a PUT with an empty content body
// is functionally equivalent to deleting an account data type directly.
func deleteUserAccountData(t *testing.T, c *client.CSAPI, viaDelete bool) {
	// Retrieve a sync token for this user
	_, nextBatchToken := c.MustSync(
		t,
		client.SyncReq{},
	)

	// Delete user account data
	if viaDelete {
		// Delete via the DELETE method
		c.MustDo(
			t,
			"DELETE",
			[]string{"_matrix", "client", "unstable", "org.matrix.msc3391", "user", c.UserID, "account_data", testAccountDataType},
		)
	} else {
		// Delete via the PUT method. PUT'ing with an empty dictionary will delete
		// the account data type for this user.
		c.SetGlobalAccountData(t, testAccountDataType, map[string]interface{}{})
	}

	// Check that the content of the user account data for this type
	// has been set to an empty dictionary.
	c.MustSyncUntil(
		t,
		client.SyncReq{
			Since: nextBatchToken,
		},
		client.SyncGlobalAccountDataHas(checkEmptyAccountData),
	)

	// Also check the account data item is no longer found
	res := c.Do(t, "GET", []string{"_matrix", "client", "v3", "user", c.UserID, "account_data", testAccountDataType})
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 404,
	})

	// Finally, check that the account data item does not appear at all in an initial sync
	initialSyncResponse, _ := c.MustSync(
		t,
		client.SyncReq{},
	)
	must.MatchGJSON(
		t,
		initialSyncResponse,
		match.JSONArrayEach(
			"account_data.events",
			checkAccountDataTypeNotPresent,
		),
	)
}

// deleteRoomAccountData removes account data for a user.
//
// If viaDelete is true, a request is made to the DELETE endpoint for room
// account data. Otherwise, the PUT method is used with an empty content
// dictionary instead. MSC3391 specifies that a PUT with an empty content body
// is functionally equivalent to deleting an account data type directly.
func deleteRoomAccountData(t *testing.T, c *client.CSAPI, viaDelete bool, roomID string) {
	// Retrieve a sync token for this user
	_, nextBatchToken := c.MustSync(
		t,
		client.SyncReq{},
	)

	// Delete room account data
	if viaDelete {
		// Delete via the DELETE method
		c.MustDo(
			t,
			"DELETE",
			[]string{"_matrix", "client", "unstable", "org.matrix.msc3391", "user", c.UserID, "rooms", roomID, "account_data", testAccountDataType},
		)
	} else {
		// Delete via the PUT method. PUT'ing with an empty dictionary will delete
		// the account data type for this room.
		c.SetRoomAccountData(t, roomID, testAccountDataType, map[string]interface{}{})
	}

	// Check that the content of the room account data for this type
	// has been set to an empty dictionary.
	c.MustSyncUntil(
		t,
		client.SyncReq{
			Since: nextBatchToken,
		},
		client.SyncRoomAccountDataHas(roomID, checkEmptyAccountData),
	)

	// Also check the account data item is no longer found
	res := c.Do(t, "GET", []string{"_matrix", "client", "v3", "user", c.UserID, "room", roomID, "account_data", testAccountDataType})
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 404,
	})

	// Finally, check that the account data item does not appear at all in an initial sync
	initialSyncResponse, _ := c.MustSync(
		t,
		client.SyncReq{},
	)
	must.MatchGJSON(
		t,
		initialSyncResponse,
		match.JSONArrayEach(
			fmt.Sprintf("rooms.join.%s.account_data.events", roomID),
			checkAccountDataTypeNotPresent,
		),
	)
}

// checkAccountDataContent checks that the content of a user or account data object
// matches our test content.
func checkAccountDataContent(r gjson.Result) bool {
	// Only listen for our test type
	if r.Get("type").Str != testAccountDataType {
		return false
	}
	content := r.Get("content")

	// Ensure the content of this account data type is as we expect
	return match.JSONDeepEqual([]byte(content.Raw), testAccountDataContent)
}

// checkEmptyAccountData checks that the content of a user or account data object
// matches our test content.
func checkEmptyAccountData(r gjson.Result) bool {
	// Only listen for our test type
	if r.Get("type").Str != testAccountDataType {
		return false
	}
	content := r.Get("content")

	// Ensure the content of this account data type is an empty map.
	// This means that it has been deleted.
	return match.JSONDeepEqual([]byte(content.Raw), map[string]interface{}{})
}

// checkAccountDataTypeNotPresent checks that a given account data event type is not present
func checkAccountDataTypeNotPresent(r gjson.Result) error {
	// If we see our test type, return a failure
	if r.Get("type").Str == testAccountDataType {
		return fmt.Errorf(
			"Found unexpected account data type '%s' in sync response",
			testAccountDataType,
		)
	}

	// We did not see our test type.
	return nil
}