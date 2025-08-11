package tests

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/tidwall/gjson"
)

var maxCanonicalJSONInt = math.Pow(2, 53) - 1

// Test that the creator can kick an admin created both via
// trusted_private_chat and by explicit promotion, including beyond PL100.
// Also checks the creator isn't in the PL event.
func TestMSC4289PrivilegedRoomCreators(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
	})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "bob",
	})

	kickBob := func(roomID string) {
		t.Helper()
		alice.MustDo(t,
			"POST", []string{"_matrix", "client", "v3", "rooms", roomID, "kick"},
			client.WithJSONBody(t, map[string]any{
				"user_id": bob.UserID,
			}),
		)
	}

	t.Run("PL event is missing creator in users map", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{
			"room_version": roomVersion12,
		})
		content := alice.MustGetStateEventContent(t, roomID, spec.MRoomPowerLevels, "")
		must.MatchGJSON(t, content, match.JSONKeyEqual("users", map[string]any{}))
	})

	t.Run("m.room.tombstone needs PL150 in the PL event", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{
			"room_version": roomVersion12,
		})
		content := alice.MustGetStateEventContent(t, roomID, spec.MRoomPowerLevels, "")
		must.MatchGJSON(t, content, match.JSONKeyEqual("events."+client.GjsonEscape("m.room.tombstone"), 150))
	})

	t.Run("creator cannot set self in PL event", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{
			"room_version": roomVersion12,
		})
		resp := alice.Do(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "state", spec.MRoomPowerLevels, ""}, client.WithJSONBody(t, map[string]any{
			"users": map[string]int{
				alice.UserID: 100,
			},
		}))
		must.MatchResponse(t, resp, match.HTTPResponse{
			StatusCode: 400,
		})
	})

	t.Run("creator can kick admin", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{
			"room_version": roomVersion12,
			"invite":       []string{bob.UserID},
		})
		bob.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})
		alice.SendEventSynced(t, roomID, b.Event{
			Type:     spec.MRoomPowerLevels,
			StateKey: b.Ptr(""),
			Content: map[string]interface{}{
				"users": map[string]any{
					bob.UserID: 100,
				},
			},
		})
		kickBob(roomID)
	})
	t.Run("creator can kick admin above PL100", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{
			"room_version": roomVersion12,
			"invite":       []string{bob.UserID},
		})
		bob.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})
		alice.SendEventSynced(t, roomID, b.Event{
			Type:     spec.MRoomPowerLevels,
			StateKey: b.Ptr(""),
			Content: map[string]interface{}{
				"users": map[string]any{
					bob.UserID: 949342,
				},
			},
		})
		kickBob(roomID)
	})
	t.Run("creator can kick admin at JSON max value", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{
			"room_version": roomVersion12,
			"invite":       []string{bob.UserID},
		})
		bob.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})
		alice.SendEventSynced(t, roomID, b.Event{
			Type:     spec.MRoomPowerLevels,
			StateKey: b.Ptr(""),
			Content: map[string]interface{}{
				"users": map[string]any{
					bob.UserID: maxCanonicalJSONInt,
				},
			},
		})
		kickBob(roomID)
	})
	// technically not a MSC4289 thing but implementations may set the creator PL to be
	// above the value expressible in canonical JSON to implement "infinite".
	t.Run("power level cannot be set beyond max canonical JSON int", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{
			"room_version": roomVersion12,
			"preset":       "public_chat",
			"invite":       []string{bob.UserID},
		})
		bob.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})
		resp := alice.Do(
			t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "state", spec.MRoomPowerLevels, ""},
			client.WithJSONBody(t, map[string]interface{}{
				"users": map[string]any{
					bob.UserID: maxCanonicalJSONInt + 1,
				},
			}),
		)
		must.MatchResponse(t, resp, match.HTTPResponse{
			StatusCode: 400,
		})
	})
	t.Run("admin with >PL100 cannot kick creator", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{
			"room_version": roomVersion12,
			"invite":       []string{bob.UserID},
		})
		bob.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})
		alice.SendEventSynced(t, roomID, b.Event{
			Type:     spec.MRoomPowerLevels,
			StateKey: b.Ptr(""),
			Content: map[string]interface{}{
				"users": map[string]any{
					bob.UserID: maxCanonicalJSONInt,
				},
			},
		})
		resp := bob.Do(t,
			"POST", []string{"_matrix", "client", "v3", "rooms", roomID, "kick"},
			client.WithJSONBody(t, map[string]any{
				"user_id": alice.UserID,
			}),
		)
		must.MatchResponse(t, resp, match.HTTPResponse{
			StatusCode: 403,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_FORBIDDEN"),
			},
		})
	})
	t.Run("admin with >PL100 sorts after the room creator for state resolution", func(t *testing.T) {
		srv := federation.NewServer(t, deployment,
			federation.HandleKeyRequests(),
			federation.HandleMakeSendJoinRequests(),
			federation.HandleTransactionRequests(nil, nil),
			federation.HandleEventRequests(),
		)
		srv.UnexpectedRequestsAreErrors = false
		cancel := srv.Listen()
		defer cancel()
		bob := srv.UserID("bob")
		roomID := alice.MustCreateRoom(t, map[string]interface{}{
			"room_version": roomVersion12,
			"preset":       "public_chat",
		})
		room := srv.MustJoinRoom(t, deployment, "hs1", roomID, bob, federation.WithRoomOpts(federation.WithImpl(&V12ServerRoom)))
		plEventID := alice.SendEventSynced(t, roomID, b.Event{
			Type:     spec.MRoomPowerLevels,
			StateKey: b.Ptr(""),
			Content: map[string]interface{}{
				"users": map[string]any{
					bob: 9493420,
				},
			},
		})
		room.WaiterForEvent(plEventID).Waitf(t, 5*time.Second, "failed to see PL event giving bob >PL100")

		alice.SendEventSynced(t, roomID, b.Event{
			Type:     spec.MRoomJoinRules,
			StateKey: b.Ptr(""),
			Content: map[string]any{
				"join_rule": spec.Invite,
			},
		})
		// Bob concurrently sets the join rule to 'knock'.
		// State resolution will apply power events (join rules) from highest PL to lowest
		// so ensure the end result is Bob's 'knock'.
		bobJREvent := srv.MustCreateEvent(t, room, federation.Event{
			Type:     spec.MRoomJoinRules,
			StateKey: b.Ptr(""),
			Content: map[string]any{
				"join_rule": spec.Knock,
			},
			PrevEvents: []string{plEventID},
			Sender:     bob,
		})
		room.AddEvent(bobJREvent)

		srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{bobJREvent.JSON()}, nil)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, bobJREvent.EventID()))
		joinRuleContent := alice.MustGetStateEventContent(t, roomID, spec.MRoomJoinRules, "")
		must.MatchGJSON(t, joinRuleContent, match.JSONKeyEqual("join_rule", "knock"))
	})
	// Some servers may apply validation to ensure the creator appears in the power_level_content_override,
	// which for v12 rooms is wrong.
	t.Run("power_level_content_override can be set", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{
			"room_version": roomVersion12,
			"invite":       []string{bob.UserID},
			"power_level_content_override": map[string]any{
				"users": map[string]int{
					bob.UserID: 100,
				},
			},
		})
		plContent := alice.MustGetStateEventContent(t, roomID, spec.MRoomPowerLevels, "")
		must.MatchGJSON(t, plContent, match.JSONKeyEqual("users", map[string]float64{
			bob.UserID: 100,
		}))
	})
	t.Run("power_level_content_override cannot set the room creator", func(t *testing.T) {
		resp := alice.CreateRoom(t, map[string]interface{}{
			"room_version": roomVersion12,
			"invite":       []string{bob.UserID},
			"power_level_content_override": map[string]any{
				"users": map[string]int{
					alice.UserID: 100,
				},
			},
		})
		must.MatchResponse(t, resp, match.HTTPResponse{
			StatusCode: 400,
		})
	})
}

// Check that additional_creators works in the happy case
func TestMSC4289PrivilegedRoomCreators_Additional(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
	})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "bob",
	})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"room_version": roomVersion12,
		"preset":       "public_chat",
		"creation_content": map[string]any{
			"additional_creators": []string{bob.UserID},
		},
	})
	bob.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})
	// we should not be able to kick bob
	res := alice.Do(t,
		"POST", []string{"_matrix", "client", "v3", "rooms", roomID, "kick"},
		client.WithJSONBody(t, map[string]any{
			"user_id": bob.UserID,
		}),
	)
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 403,
		JSON: []match.JSON{
			match.JSONKeyEqual("errcode", "M_FORBIDDEN"),
		},
	})
	// Bob should be able to do privileged operations like set the room name
	bob.SendEventSynced(t, roomID, b.Event{
		Type:     spec.MRoomName,
		StateKey: b.Ptr(""),
		Content: map[string]any{
			"name": "Bob's room name",
		},
	})
	// Bob should not be able to be inserted into content.users in the PL event
	// because they are in additional_creators
	resp := alice.Do(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "state", spec.MRoomPowerLevels, ""}, client.WithJSONBody(t, map[string]any{
		"users": map[string]int{
			bob.UserID: 100,
		},
	}))
	must.MatchResponse(t, resp, match.HTTPResponse{
		StatusCode: 400,
	})
}

func TestMSC4289PrivilegedRoomCreators_InvitedAreCreators(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
	})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "bob",
	})
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"room_version": roomVersion12,
		"preset":       "trusted_private_chat",
		"is_direct":    true,
		"invite":       []string{bob.UserID},
	})
	createContent := alice.MustGetStateEventContent(t, roomID, spec.MRoomCreate, "")
	must.MatchGJSON(t, createContent, match.JSONKeyEqual("additional_creators", []string{bob.UserID}))
}

// Ensure that trusted_private_chat handling doesn't replace additional_creators
func TestMSC4289PrivilegedRoomCreators_AdditionalCreatorsAndInvited(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
	})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "bob",
	})
	charlie := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "charlie",
	})
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"room_version": roomVersion12,
		"preset":       "trusted_private_chat",
		"is_direct":    true,
		"invite":       []string{bob.UserID},
		"creation_content": map[string]any{
			"additional_creators": []string{charlie.UserID},
		},
	})
	createContent := alice.MustGetStateEventContent(t, roomID, spec.MRoomCreate, "")
	must.MatchGJSON(t, createContent,
		match.JSONCheckOff("additional_creators", []interface{}{bob.UserID, charlie.UserID}, match.CheckOffMapper(func(r gjson.Result) interface{} {
			return r.Str
		})),
	)
}

// Check that 'additional_creators' is validated correctly.
func TestMSC4289PrivilegedRoomCreators_AdditionalValidation(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
	})

	testCases := []struct {
		Name               string
		AdditionalCreators any
		WantSuccess        bool
	}{
		{
			Name:               "additional_creators isn't an array",
			AdditionalCreators: "not-an-array",
			WantSuccess:        false,
		},
		{
			Name:               "additional_creators elements aren't strings",
			AdditionalCreators: []any{"@foo:example.com", 42},
			WantSuccess:        false,
		},
		{
			Name:               "additional_creators elements aren't user ID strings",
			AdditionalCreators: []any{"@foo:example.com", "not-a-user-id"},
			WantSuccess:        false,
		},
		{
			Name:               "additional_creators elements aren't valid user ID strings (domain)",
			AdditionalCreators: []any{"@invalid:dom$ain$.com"},
			WantSuccess:        false,
		},
		{
			Name:               "additional_creators are valid",
			AdditionalCreators: []any{"@foo:example.com", "@bar:baz.code"},
			WantSuccess:        true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			resp := alice.CreateRoom(t, map[string]interface{}{
				"room_version": roomVersion12,
				"preset":       "public_chat",
				"creation_content": map[string]any{
					"additional_creators": tc.AdditionalCreators,
				},
			})
			if tc.WantSuccess {
				must.MatchResponse(t, resp, match.HTTPResponse{
					StatusCode: 200,
				})
			} else {
				must.MatchResponse(t, resp, match.HTTPResponse{
					StatusCode: 400,
				})
			}
		})
	}
}

func TestMSC4289PrivilegedRoomCreators_Upgrades(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
	})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "bob",
	})
	charlie := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "charlie",
	})

	testCases := []struct {
		name                      string
		initialCreator            *client.CSAPI
		initialAdditionalCreators []string
		initialVersion            string
		initialUserPLs            map[string]int
		entitiyDoingUpgrade       *client.CSAPI
		newAdditionalCreators     []string
		// assertions
		wantAdditionalCreators []string
		wantNewUsersMap        map[string]int64
	}{
		{
			name:           "non-creator admins can upgrade v11 rooms to v12",
			initialCreator: alice,
			initialVersion: "11",
			initialUserPLs: map[string]int{
				bob.UserID: 100,
			},
			entitiyDoingUpgrade:    bob,
			wantAdditionalCreators: []string{},
			wantNewUsersMap:        map[string]int64{},
		},
		{
			name:           "non-creator admins can upgrade v11 rooms to v12 with additional moderators",
			initialCreator: alice,
			initialVersion: "11",
			initialUserPLs: map[string]int{
				bob.UserID:     100,
				charlie.UserID: 100,
			},
			entitiyDoingUpgrade:    bob,
			wantAdditionalCreators: []string{},
			wantNewUsersMap: map[string]int64{
				charlie.UserID: 100,
			},
		},
		{
			name:           "non-creator admins can upgrade v12 rooms to v12 with different creators",
			initialCreator: alice,
			initialVersion: roomVersion12,
			initialUserPLs: map[string]int{
				bob.UserID: 150, // bob has enough permission to upgrade
			},
			entitiyDoingUpgrade:    bob,
			newAdditionalCreators:  []string{charlie.UserID},
			wantAdditionalCreators: []string{charlie.UserID},
			wantNewUsersMap:        map[string]int64{},
		},
		{
			name:           "non-creator admins can upgrade v12 rooms to v12 with different creators with additional moderators",
			initialCreator: alice,
			initialVersion: roomVersion12,
			initialUserPLs: map[string]int{
				bob.UserID:     150, // bob has enough permission to upgrade
				charlie.UserID: 50,  // gets removed as he will become an additional creator
			},
			entitiyDoingUpgrade:    bob,
			newAdditionalCreators:  []string{charlie.UserID},
			wantAdditionalCreators: []string{charlie.UserID},
			wantNewUsersMap:        map[string]int64{},
		},
		{
			name:           "creator admins can upgrade v11 rooms to v12 with additional_creators",
			initialCreator: alice,
			initialVersion: "11",
			initialUserPLs: map[string]int{
				alice.UserID: 100,
				bob.UserID:   100,
			},
			entitiyDoingUpgrade:    alice,
			newAdditionalCreators:  []string{bob.UserID},
			wantAdditionalCreators: []string{bob.UserID},
			wantNewUsersMap:        map[string]int64{}, // both alice and bob are removed as they are now creators.
		},
		{
			name:           "creator admins can upgrade v11 rooms to v12 with additional_creators and moderators",
			initialCreator: alice,
			initialVersion: "11",
			initialUserPLs: map[string]int{
				alice.UserID:   100,
				bob.UserID:     100,
				charlie.UserID: 50,
			},
			entitiyDoingUpgrade:    alice,
			newAdditionalCreators:  []string{bob.UserID},
			wantAdditionalCreators: []string{bob.UserID},
			wantNewUsersMap: map[string]int64{
				charlie.UserID: 50,
			},
		},
	}

	for _, tc := range testCases {
		createBody := map[string]interface{}{
			"room_version": tc.initialVersion,
			"preset":       "public_chat",
		}
		if tc.initialAdditionalCreators != nil {
			must.Equal(t, tc.initialVersion, roomVersion12, "can only set additional_creators on v12")
			createBody["additional_creators"] = tc.initialAdditionalCreators
		}
		roomID := tc.initialCreator.MustCreateRoom(t, createBody)
		alice.JoinRoom(t, roomID, []spec.ServerName{"hs1"})
		bob.JoinRoom(t, roomID, []spec.ServerName{"hs1"})
		charlie.JoinRoom(t, roomID, []spec.ServerName{"hs1"})
		tc.initialCreator.SendEventSynced(t, roomID, b.Event{
			Type:     spec.MRoomPowerLevels,
			StateKey: b.Ptr(""),
			Content: map[string]interface{}{
				"users": tc.initialUserPLs,
			},
		})
		upgradeBody := map[string]any{
			"new_version": roomVersion12,
		}
		if tc.newAdditionalCreators != nil {
			upgradeBody["additional_creators"] = tc.newAdditionalCreators
		}
		res := tc.entitiyDoingUpgrade.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "upgrade"}, client.WithJSONBody(t, upgradeBody))
		newRoomID := must.ParseJSON(t, res.Body).Get("replacement_room").Str
		// New Create event assertions
		createContent := tc.entitiyDoingUpgrade.MustGetStateEventContent(t, newRoomID, spec.MRoomCreate, "")
		createAssertions := []match.JSON{
			match.JSONKeyEqual("room_version", roomVersion12),
		}
		if tc.wantAdditionalCreators != nil {
			if len(tc.wantAdditionalCreators) > 0 {
				createAssertions = append(createAssertions, match.JSONKeyEqual("additional_creators", tc.wantAdditionalCreators))
			} else {
				createAssertions = append(createAssertions, match.JSONKeyMissing("additional_creators"))
			}
		}
		must.MatchGJSON(
			t, createContent, createAssertions...,
		)
		// New PL assertions
		plContent := tc.entitiyDoingUpgrade.MustGetStateEventContent(t, newRoomID, spec.MRoomPowerLevels, "")
		if tc.wantNewUsersMap != nil {
			plContent.Get("users").ForEach(func(key, v gjson.Result) bool {
				gotVal := v.Int()
				wantVal, ok := tc.wantNewUsersMap[key.Str]
				if !ok {
					ct.Errorf(t, "%s: upgraded room PL content, user %s has PL %v but want it missing", tc.name, key.Str, gotVal)
					return true
				}
				if gotVal != wantVal {
					ct.Errorf(t, "%s: upgraded room PL content, user %s has PL %v want %v", tc.name, key.Str, gotVal, wantVal)
				}
				delete(tc.wantNewUsersMap, key.Str)
				return true
			})
			if len(tc.wantNewUsersMap) > 0 {
				ct.Errorf(t, "%s: upgraded room PL content missed these users %v", tc.name, tc.wantNewUsersMap)
			}
		}
		t.Logf("OK: %v", tc.name)
	}
}
