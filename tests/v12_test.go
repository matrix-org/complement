package tests

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"slices"
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
	"github.com/matrix-org/complement/runtime"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/matrix-org/util"
	"github.com/tidwall/gjson"
)

var maxCanonicalJSONInt = math.Pow(2, 53) - 1

const roomVersion12 = "12"

var V12ServerRoom = federation.ServerRoomImplCustom{
	ProtoEventCreatorFn: Protov12EventCreator,
}

// Override how Complement makes proto events so we can conditionally disable/enable the inclusion of the create event
// depending on whether we're running in combined mode or not.
// Complement also doesn't set the room version correctly on the ProtoEvent as this was a new addition to GMSL.
func Protov12EventCreator(def federation.ServerRoomImpl, room *federation.ServerRoom, ev federation.Event) (*gomatrixserverlib.ProtoEvent, error) {
	var prevEvents interface{}
	if ev.PrevEvents != nil {
		// We deliberately want to set the prev events.
		prevEvents = ev.PrevEvents
	} else {
		// No other prev events were supplied so we'll just
		// use the forward extremities of the room, which is
		// the usual behaviour.
		prevEvents = room.ForwardExtremities
	}
	proto := gomatrixserverlib.ProtoEvent{
		SenderID:   ev.Sender,
		Depth:      int64(room.Depth + 1), // depth starts at 1
		Type:       ev.Type,
		StateKey:   ev.StateKey,
		RoomID:     room.RoomID,
		PrevEvents: prevEvents,
		AuthEvents: ev.AuthEvents,
		Redacts:    ev.Redacts,
		Version:    gomatrixserverlib.MustGetRoomVersion(room.Version),
	}
	if err := proto.SetContent(ev.Content); err != nil {
		return nil, fmt.Errorf("EventCreator: failed to marshal event content: %s - %+v", err, ev.Content)
	}
	if err := proto.SetUnsigned(ev.Content); err != nil {
		return nil, fmt.Errorf("EventCreator: failed to marshal event unsigned: %s - %+v", err, ev.Unsigned)
	}
	if proto.AuthEvents == nil {
		var stateNeeded gomatrixserverlib.StateNeeded
		// this does the right thing for v12
		stateNeeded, err := gomatrixserverlib.StateNeededForProtoEvent(&proto)
		if err != nil {
			return nil, fmt.Errorf("EventCreator: failed to work out auth_events : %s", err)
		}
		// we never include the create event if the HS supports MSC4291
		stateNeeded.Create = false
		proto.AuthEvents = room.AuthEvents(stateNeeded)
	}
	return &proto, nil
}

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
		initialVersion            string
		initialUserPLs            map[string]int
		entityDoingUpgrade       *client.CSAPI
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
			entityDoingUpgrade:    bob,
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
			entityDoingUpgrade:    bob,
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
			entityDoingUpgrade:    bob,
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
			entityDoingUpgrade:    bob,
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
			entityDoingUpgrade:    alice,
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
			entityDoingUpgrade:    alice,
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
		res := tc.entityDoingUpgrade.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "upgrade"}, client.WithJSONBody(t, upgradeBody))
		newRoomID := must.ParseJSON(t, res.Body).Get("replacement_room").Str
		// New Create event assertions
		createContent := tc.entityDoingUpgrade.MustGetStateEventContent(t, newRoomID, spec.MRoomCreate, "")
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
		plContent := tc.entityDoingUpgrade.MustGetStateEventContent(t, newRoomID, spec.MRoomPowerLevels, "")
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

func TestMSC4289PrivilegedRoomCreators_Downgrades(t *testing.T) {
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

	max_canonicaljson_power_level := int64(math.Pow(2, 53) - 1)

	testCases := []struct {
		name                      string
		initialCreator            *client.CSAPI
		initialAdditionalCreators []string
		newVersion                string
		initialPLs                map[string]any
		entityDoingUpgrade       *client.CSAPI
		// assertions
		wantNewUsersMap        map[string]int64
	}{
		{
			name:           "upgrading a room from v12 to v11 sets the old room's creator to the max canonicaljson power level in the new room",
			initialCreator: alice,
			newVersion: "11",
			initialPLs: map[string]any{},
			entityDoingUpgrade:    alice,
			wantNewUsersMap:        map[string]int64{
				// Max canonicaljson power level.
				alice.UserID: max_canonicaljson_power_level,
			},
		},
		{
			name:           "upgrading a room from v12 to v11 keeps the additional_creator users as admins of the new room. existing users remain the same",
			initialCreator: alice,
			initialAdditionalCreators: []string{
				bob.UserID,
			},
			newVersion: "11",
			initialPLs: map[string]any{
				"users": map[string]int64{
					charlie.UserID: 30,
				},
			},
			entityDoingUpgrade:    alice,
			wantNewUsersMap:        map[string]int64{
				alice.UserID: max_canonicaljson_power_level,
				bob.UserID: max_canonicaljson_power_level,
				charlie.UserID: 30, // charlie is not a creator
			},
		},
		{
			name:           "upgrading a room from v12 to v11 sets any user who wasn't a creator, but had max canonicaljson power level in the old room, to just below the max canonicaljson power level in the new room",
			initialCreator: alice,
			newVersion: "11",
			initialPLs: map[string]any{
				"users": map[string]int64{
					bob.UserID:   30,
					charlie.UserID: max_canonicaljson_power_level,
				},
			},
			entityDoingUpgrade:    alice,
			wantNewUsersMap:        map[string]int64{
				// Neither bob or charlie are creators.
				alice.UserID: max_canonicaljson_power_level,
				bob.UserID: 30,
				// charlie's power level was reduced just below max canonicaljson int.
				charlie.UserID: max_canonicaljson_power_level - 1,
			},
		},
	}

	for _, tc := range testCases {
		createBody := map[string]interface{}{
			"room_version": roomVersion12,
			"preset":       "public_chat",
		}
		if tc.initialAdditionalCreators != nil {
			createBody["creation_content"] = map[string]any{
				"additional_creators": tc.initialAdditionalCreators,
			}
		}
		roomID := tc.initialCreator.MustCreateRoom(t, createBody)
		alice.JoinRoom(t, roomID, []spec.ServerName{"hs1"})
		bob.JoinRoom(t, roomID, []spec.ServerName{"hs1"})
		tc.initialCreator.SendEventSynced(t, roomID, b.Event{
			Type:     spec.MRoomPowerLevels,
			StateKey: b.Ptr(""),
			Content: tc.initialPLs,
		})
		upgradeBody := map[string]any{
			"new_version": tc.newVersion,
		}
		res := tc.entityDoingUpgrade.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "upgrade"}, client.WithJSONBody(t, upgradeBody))
		newRoomID := must.ParseJSON(t, res.Body).Get("replacement_room").Str
		// New Create event assertions
		createContent := tc.entityDoingUpgrade.MustGetStateEventContent(t, newRoomID, spec.MRoomCreate, "")
		createAssertions := []match.JSON{
			match.JSONKeyEqual("room_version", tc.newVersion),
		}
		must.MatchGJSON(
			t, createContent, createAssertions...,
		)
		// New PL assertions
		plContent := tc.entityDoingUpgrade.MustGetStateEventContent(t, newRoomID, spec.MRoomPowerLevels, "")
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
				ct.Fatalf(t, "%s: upgraded room PL content missed these users %v", tc.name, tc.wantNewUsersMap)
			}
		}
		t.Logf("OK: %v", tc.name)
	}
}

// Test that the room ID is in fact the hash of the create event.
func TestMSC4291RoomIDAsHashOfCreateEvent(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"room_version": roomVersion12,
	})
	assertCreateEventIsRoomID(t, alice, roomID)
}

func TestMSC4291RoomIDAsHashOfCreateEvent_AuthEventsOmitsCreateEvent(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"room_version": roomVersion12,
		"preset":       "public_chat",
	})

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

	room := srv.MustJoinRoom(t, deployment, "hs1", roomID, bob, federation.WithRoomOpts(federation.WithImpl(&V12ServerRoom)))

	createEvent := room.CurrentState(spec.MRoomCreate, "")
	if createEvent == nil {
		ct.Fatalf(t, "missing create event from /send_join response")
	}
	t.Logf("Create event is %s", createEvent.EventID())
	createEventID := createEvent.EventID()
	must.Equal(t,
		roomID, fmt.Sprintf("!%s", createEventID[1:]), // swap $ for !
		"room ID was not the hash of the create event ID",
	)

	for _, event := range room.Timeline {
		rawAuthEvents := gjson.GetBytes(event.JSON(), "auth_events")
		must.Equal(t, rawAuthEvents.IsArray(), true, "auth_events key is missing / not an array")
		var authEventIDs []string
		for _, rawAuthEventID := range rawAuthEvents.Array() {
			authEventIDs = append(authEventIDs, rawAuthEventID.Str)
		}
		t.Logf("create=%v authEventIDs=>%v", createEvent.EventID(), authEventIDs)
		if slices.Contains(authEventIDs, createEvent.EventID()) {
			ct.Fatalf(t, "Event %s (%s) contains the create event in auth_events: %v", event.EventID(), event.Type(), authEventIDs)
		}
		must.Equal(t, event.RoomID().String(), roomID, fmt.Sprintf("event %s room ID mismatch: got %v want %v", event.EventID(), event.RoomID(), roomID))
	}
}

// Test that /upgrade also makes a room where the create event ID is the room ID
func TestMSC4291RoomIDAsHashOfCreateEvent_UpgradedRooms(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "alice"})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "bob"})

	testCases := []struct {
		initialVersion string
	}{
		{
			initialVersion: roomVersion12,
		},
		{
			initialVersion: "11",
		},
		{
			initialVersion: "10",
		},
	}
	for _, tc := range testCases {
		oldRoomID := alice.MustCreateRoom(t, map[string]interface{}{
			"room_version": tc.initialVersion,
			"preset":       "public_chat",
		})
		bob.MustJoinRoom(t, oldRoomID, []spec.ServerName{"hs1"})
		res := alice.MustDo(t, "POST", []string{
			"_matrix", "client", "v3", "rooms", oldRoomID, "upgrade",
		}, client.WithJSONBody(t, map[string]any{
			"new_version": roomVersion12,
		}))
		newRoomID := gjson.GetBytes(client.ParseJSON(t, res), "replacement_room").Str
		t.Logf("upgraded from %s (%s) to %s (%s)", tc.initialVersion, oldRoomID, roomVersion12, newRoomID)
		assertCreateEventIsRoomID(t, alice, newRoomID)
		tombstoneContent := alice.MustGetStateEventContent(t, oldRoomID, "m.room.tombstone", "")
		must.MatchGJSON(t, tombstoneContent, match.JSONKeyEqual("replacement_room", newRoomID))
		createContent := alice.MustGetStateEventContent(t, newRoomID, spec.MRoomCreate, "")
		must.MatchGJSON(t, createContent, match.JSONKeyEqual("predecessor.room_id", oldRoomID), match.JSONKeyMissing("predecessor.event_id"))
	}
}

// Ensure that clients cannot send an m.room.create event in an existing room.
func TestMSC4291RoomIDAsHashOfCreateEvent_CannotSendCreateEvent(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	for _, version := range []string{"11", roomVersion12} {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{
			"room_version": version,
		})
		resp := alice.Do(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "state", spec.MRoomCreate, ""}, client.WithJSONBody(t, map[string]any{
			"room_version": version,
			// some homeservers may not create a new event if the content exactly matches the prior state,
			// so just add some entropy.
			"entropy": 100,
		}))
		must.MatchResponse(t, resp, match.HTTPResponse{StatusCode: 400})
	}
}

// Test that all CS APIs that return events include the room_id for the create event,
// with the exception of /sync as that always removes room IDs.
func TestMSC4291RoomIDAsHashOfCreateEvent_RoomIDIsOnCreateEvent(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"room_version": roomVersion12,
	})
	eventID := alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello",
		},
	})
	createEventID := "$" + roomID[1:]

	testCases := []struct {
		name               string
		path               []string
		qps                url.Values
		extractCreateEvent func(resp gjson.Result) *gjson.Result
	}{
		{
			name: "/state",
			path: []string{"_matrix", "client", "v3", "rooms", roomID, "state"},
			extractCreateEvent: func(resp gjson.Result) *gjson.Result {
				for _, ev := range resp.Array() {
					if ev.Get("type").Str == spec.MRoomCreate {
						return &ev
					}
				}
				return nil
			},
		},
		{
			name: "/messages",
			path: []string{"_matrix", "client", "v3", "rooms", roomID, "messages"},
			qps: url.Values{
				"dir":   {"b"},
				"limit": {"100"},
			},
			extractCreateEvent: func(resp gjson.Result) *gjson.Result {
				for _, ev := range resp.Get("chunk").Array() {
					if ev.Get("type").Str == spec.MRoomCreate {
						return &ev
					}
				}
				return nil
			},
		},
		{
			name: "/event/{eventID}",
			path: []string{"_matrix", "client", "v3", "rooms", roomID, "event", createEventID},
			extractCreateEvent: func(resp gjson.Result) *gjson.Result {
				return &resp
			},
		},
		{
			name: "/context direct",
			path: []string{"_matrix", "client", "v3", "rooms", roomID, "context", createEventID},
			extractCreateEvent: func(resp gjson.Result) *gjson.Result {
				ev := resp.Get("event")
				return &ev
			},
		},
		{
			name: "/context indirect",
			qps: url.Values{
				"limit": {"100"},
			},
			path: []string{"_matrix", "client", "v3", "rooms", roomID, "context", eventID},
			extractCreateEvent: func(resp gjson.Result) *gjson.Result {
				for _, ev := range resp.Get("events_before").Array() {
					if ev.Get("type").Str == spec.MRoomCreate {
						return &ev
					}
				}
				return nil
			},
		},
		{
			name: "/context state",
			qps: url.Values{
				"limit": {"100"},
			},
			path: []string{"_matrix", "client", "v3", "rooms", roomID, "context", eventID},
			extractCreateEvent: func(resp gjson.Result) *gjson.Result {
				for _, ev := range resp.Get("state").Array() {
					if ev.Get("type").Str == spec.MRoomCreate {
						return &ev
					}
				}
				return nil
			},
		},
		{
			name: "/state?format=event",
			qps: url.Values{
				"format": {"event"},
			},
			path: []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.create", ""},
			extractCreateEvent: func(resp gjson.Result) *gjson.Result {
				return &resp
			},
		},
	}
	for _, tc := range testCases {
		opts := []client.RequestOpt{}
		if tc.qps != nil {
			opts = append(opts, client.WithQueries(tc.qps))
		}
		resp := alice.MustDo(t, "GET", tc.path, opts...)
		body := must.ParseJSON(t, resp.Body)
		createEvent := tc.extractCreateEvent(body)
		if createEvent == nil {
			ct.Errorf(t, "%s: failed to find create event", tc.name)
			continue
		}
		must.Equal(t, createEvent.Get("room_id").Str, roomID, fmt.Sprintf("%s: create event is missing room ID", tc.name))
	}
}

func assertCreateEventIsRoomID(t ct.TestLike, client *client.CSAPI, roomID string) (createEventID string) {
	t.Helper()
	res := client.MustDo(t, "GET", []string{
		"_matrix", "client", "v3", "rooms", roomID, "state",
	})
	stateEvents := must.ParseJSON(t, res.Body)
	stateEvents.ForEach(func(_, value gjson.Result) bool {
		if value.Get("type").Str == spec.MRoomCreate && value.Get("state_key").Str == "" {
			createEventID = value.Get("event_id").Str
			return false
		}
		return true
	})
	if createEventID == "" {
		ct.Fatalf(t, "failed to find create event ID from /state respone: %v", stateEvents.Raw)
	}
	must.Equal(t,
		roomID, fmt.Sprintf("!%s", createEventID[1:]),
		"room ID was not the hash of the create event ID",
	)
	return createEventID
}

// Test that v2.1 has implemented starting from the empty set not the unconflicted set
// This test assumes a few things about the underlying server implementation:
//   - It eventually gives up calling /get_missing_events for some n < 250 and hits /state or /state_ids for the historical state.
//   - It does not call /event_auth but may call /event/{eventID}
//   - On encountering DAG gaps, the current state is the resolution of all the forwards extremities for each section.
//     In other words, the server calculates the current state as the merger of (what_we_knew_before, what_we_know_now),
//     despite there being no events with >1 prev_events.
//
// The scenario in this test is similar to the one in the MSC but different in two key ways:
//   - To force incorrect state, "Charlie changes display name" happens 250 times to force a /state{_ids} request.
//     In the MSC this only happened once.
//   - "Bob changes display name" does not exist. We rely on the server calculating the current state as the
//     merging of the forwards extremitiy before the gap and the forwards extremity after the gap, so
//     in other words we apply state resolution to (Alice leave, 250th Charlie display name change).
func TestMSC4297StateResolutionV2_1_starts_from_empty_set(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // needs additional fixes
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(nil, nil),
		federation.HandleEventRequests(),
	)
	srv.UnexpectedRequestsAreErrors = false
	cancel := srv.Listen()
	defer cancel()
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "alice"})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "bob"})
	charlie := srv.UserID("charlie")

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"room_version": roomVersion12,
		"preset":       "public_chat",
	})
	bob.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})
	room := srv.MustJoinRoom(t, deployment, "hs1", roomID, charlie, federation.WithRoomOpts(federation.WithImpl(&V12ServerRoom)))
	joinRulePublic := room.CurrentState(spec.MRoomJoinRules, "")
	aliceJoin := room.CurrentState(spec.MRoomMember, alice.UserID)
	synchronisationEventID := bob.SendEventSynced(t, room.RoomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "can you hear me charlie?",
		},
	})
	room.WaiterForEvent(synchronisationEventID).Waitf(t, 5*time.Second, "failed to see synchronisation event, is federation working?")

	// Alice makes the room invite-only then leaves
	joinRuleInviteOnlyEventID := alice.SendEventSynced(t, roomID, b.Event{
		Type:     spec.MRoomJoinRules,
		StateKey: b.Ptr(""),
		Sender:   alice.UserID,
		Content: map[string]interface{}{
			"join_rule": "invite",
		},
	})
	room.WaiterForEvent(joinRuleInviteOnlyEventID).Waitf(t, 5*time.Second, "failed to see invite join rule event")

	alice.MustLeaveRoom(t, roomID)
	// Wait for Charlie to see it
	time.Sleep(time.Second)
	aliceLeaveEvent := room.CurrentState(spec.MRoomMember, alice.UserID)
	if membership, err := aliceLeaveEvent.Membership(); err != nil || membership != spec.Leave {
		ct.Fatalf(t, "failed to see Alice leave the room, alice event is %s", string(aliceLeaveEvent.JSON()))
	}

	// Now only Bob (server under test) and Charlie (Complement server) are left in the room.
	// Charlie is going to send an event with unknown prev_event, causing /get_missing_events
	// until eventually /state_ids is hit. When it is, we'll return incorrect room state, claiming
	// that the current join rule is public, not invite. This will cause the join rules to get conflicted
	// and replayed. V2 would base the checks off the unconflicted state, and since all servers agree
	// that Alice=leave it would start like that, making the join rule transitions invalid and causing the
	// room to have no join rule at all. V2.1 fixes this by loading the auth_events of the event being replayed
	// which correctly has Alice joined. Alice isn't automatically re-joined to the room though because the
	// last step of the algorithm is to apply the unconflicted state on top of the resolved conflicts, without
	// any extra checks.

	// We don't know how far back server impls will go, so let's use 250 as a large enough value.
	charlieEvents := make([]gomatrixserverlib.PDU, 250)
	eventIDToIndex := make(map[string]int)
	for i := range charlieEvents {
		ev := srv.MustCreateEvent(t, room, federation.Event{
			Type:     spec.MRoomMember,
			StateKey: &charlie,
			Sender:   charlie,
			Content: map[string]interface{}{
				"membership":  spec.Join,
				"displayname": fmt.Sprintf("Charlie %d", i),
			},
		})
		room.AddEvent(ev)
		charlieEvents[i] = ev
		eventIDToIndex[ev.EventID()] = i
	}

	seenStateIDs := helpers.NewWaiter()
	// process requests to walk back through charlie's display name changes
	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/get_missing_events/{roomID}",
		srv.ValidFederationRequest(t, getMissingEventsHandler(t, room, eventIDToIndex, charlieEvents)),
	)

	getIncorrectState := func(atEventID string) (authChain, stateEvents []gomatrixserverlib.PDU) {
		t.Logf("calculating state before event %v (i=%v)", atEventID, eventIDToIndex[atEventID])
		// Find the correct state for charlie
		// we want to return the state before the requested event hence -1
		stateEvents = append(stateEvents, charlieEvents[eventIDToIndex[atEventID]-1])
		// charlie's auth events are everything prior to this
		authChain = append(authChain, charlieEvents[:eventIDToIndex[atEventID]-1]...)

		stateEvents = append(stateEvents,
			room.CurrentState(spec.MRoomCreate, ""),
			room.CurrentState(spec.MRoomPowerLevels, ""),
			room.CurrentState(spec.MRoomMember, bob.UserID),
			joinRulePublic, // This is wrong, and should be invite.
			aliceLeaveEvent,
		)
		authChain = append(authChain, aliceJoin)
		return
	}
	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/state_ids/{roomID}",
		srv.ValidFederationRequest(t, func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
			if pathParams["roomID"] != room.RoomID {
				t.Errorf("Received /state_ids for the wrong room: %s", room.RoomID)
				return util.JSONResponse{
					Code: 400,
					JSON: "wrong room",
				}
			}
			defer seenStateIDs.Finish()
			req, err := fr.HTTPRequest()
			must.NotError(t, "failed to get fed http reqest", err)
			atEventID := req.URL.Query().Get("event_id")
			t.Logf("received /state_ids at event %v (i=%v)", atEventID, eventIDToIndex[atEventID])
			authChain, stateEvents := getIncorrectState(atEventID)
			return util.JSONResponse{
				Code: 200,
				JSON: struct {
					AuthChainIDs []string `json:"auth_chain_ids"`
					PDUIDs       []string `json:"pdu_ids"`
				}{
					AuthChainIDs: asEventIDs(authChain),
					PDUIDs:       asEventIDs(stateEvents),
				},
			}
		}),
	)
	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/state/{roomID}",
		srv.ValidFederationRequest(t, func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
			if pathParams["roomID"] != room.RoomID {
				t.Errorf("Received /state for the wrong room: %s", room.RoomID)
				return util.JSONResponse{
					Code: 400,
					JSON: "wrong room",
				}
			}
			defer seenStateIDs.Finish()
			req, err := fr.HTTPRequest()
			must.NotError(t, "failed to get fed http reqest", err)
			atEventID := req.URL.Query().Get("event_id")
			t.Logf("received /state at event %v (i=%v)", atEventID, eventIDToIndex[atEventID])
			authChain, stateEvents := getIncorrectState(atEventID)
			return util.JSONResponse{
				Code: 200,
				JSON: struct {
					AuthChain gomatrixserverlib.EventJSONs `json:"auth_chain"`
					PDUs      gomatrixserverlib.EventJSONs `json:"pdus"`
				}{
					AuthChain: gomatrixserverlib.NewEventJSONsFromEvents(authChain),
					PDUs:      gomatrixserverlib.NewEventJSONsFromEvents(stateEvents),
				},
			}
		}),
	)

	// Send the last event to trigger /get_missing_events and /state_ids
	finalEvent := charlieEvents[len(charlieEvents)-1]
	srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{
		finalEvent.JSON(),
	}, nil)

	seenStateIDs.Waitf(t, 5*time.Second, "failed to see a /state{_ids} request")

	// wait until bob sees the final event
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(room.RoomID, finalEvent.EventID()))

	// the join rules should be `invite`.
	// Servers which do not implement v2.1 will get a HTTP 404 here.
	content := bob.MustGetStateEventContent(t, room.RoomID, spec.MRoomJoinRules, "")
	must.MatchGJSON(t, content, match.JSONKeyEqual("join_rule", "invite"))
}

func TestMSC4297StateResolutionV2_1_includes_conflicted_subgraph(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // needs additional fixes
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(nil, nil),
		federation.HandleEventRequests(),
	)
	srv.UnexpectedRequestsAreErrors = false
	cancel := srv.Listen()
	defer cancel()
	creator := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "creator"})
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "alice"})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "bob"})
	charlie := srv.UserID("charlie")
	eve := srv.UserID("eve")
	zara := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "zara"})

	// We play every event from Problem B from the MSC except for Eve's events. We separate out
	// Alice and Creator roles for combined MSC compatibility.
	roomID := creator.MustCreateRoom(t, map[string]interface{}{
		"room_version": roomVersion12,
		"preset":       "public_chat",
	})
	creator.SendEventSynced(t, roomID, b.Event{
		Type:     spec.MRoomPowerLevels,
		StateKey: b.Ptr(""),
		Content: map[string]any{
			"users": map[string]any{
				alice.UserID: 100,
			},
		},
	})
	alice.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})
	bob.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})
	room := srv.MustJoinRoom(t, deployment, "hs1", roomID, charlie, federation.WithRoomOpts(federation.WithImpl(&V12ServerRoom)))
	firstPowerLevelEvent := room.CurrentState(spec.MRoomPowerLevels, "")
	alice.SendEventSynced(t, roomID, b.Event{
		Type:     spec.MRoomPowerLevels,
		StateKey: b.Ptr(""),
		Content: map[string]any{
			"users": map[string]any{
				alice.UserID: 100,
				bob.UserID:   50,
			},
		},
	})
	pl3EventID := bob.SendEventSynced(t, roomID, b.Event{
		Type:     spec.MRoomPowerLevels,
		StateKey: b.Ptr(""),
		Content: map[string]any{
			"users": map[string]any{
				alice.UserID: 100,
				bob.UserID:   50,
				charlie:      50,
			},
		},
	})
	zara.MustJoinRoom(t, roomID, []spec.ServerName{"hs1"})

	// Now Eve will join from the third PL event
	eveJoinEvent := srv.MustCreateEvent(t, room, federation.Event{
		Type:     spec.MRoomMember,
		StateKey: &eve,
		Sender:   eve,
		Content: map[string]interface{}{
			"membership": spec.Join,
		},
		PrevEvents: []string{
			pl3EventID,
		},
	})
	room.AddEvent(eveJoinEvent)
	srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{eveJoinEvent.JSON()}, nil)
	aliceSince := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, eveJoinEvent.EventID()))

	// Now change Eve's display name many times to force partial synchronisation

	// We don't know how far back server impls will go, so let's use 250 as a large enough value.
	eveEvents := make([]gomatrixserverlib.PDU, 250)
	eventIDToIndex := make(map[string]int)
	prevEvents := eveJoinEvent.EventID()
	for i := range eveEvents {
		ev := srv.MustCreateEvent(t, room, federation.Event{
			Type:     spec.MRoomMember,
			StateKey: &eve,
			Sender:   eve,
			Content: map[string]interface{}{
				"membership":  spec.Join,
				"displayname": fmt.Sprintf("Eve %d", i),
			},
			PrevEvents: []string{prevEvents},
		})
		room.AddEvent(ev)
		eveEvents[i] = ev
		eventIDToIndex[ev.EventID()] = i
		prevEvents = ev.EventID()
	}

	seenStateIDs := helpers.NewWaiter()

	// process requests to walk back through eve's display name changes
	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/get_missing_events/{roomID}",
		srv.ValidFederationRequest(t, getMissingEventsHandler(t, room, eventIDToIndex, eveEvents)),
	)

	getIncorrectState := func(atEventID string) (authChain, stateEvents []gomatrixserverlib.PDU) {
		t.Logf("calculating state before event %v (i=%v)", atEventID, eventIDToIndex[atEventID])
		// Find the correct state for eve
		// we want to return the state before the requested event hence -1
		stateEvents = append(stateEvents, eveEvents[eventIDToIndex[atEventID]-1])
		// charlie's auth events are everything prior to this
		authChain = append(authChain, eveEvents[:eventIDToIndex[atEventID]-1]...)

		stateEvents = append(stateEvents,
			room.CurrentState(spec.MRoomCreate, ""),
			room.CurrentState(spec.MRoomMember, alice.UserID),
			firstPowerLevelEvent, // This is wrong, and should be the 3rd PL event.
			room.CurrentState(spec.MRoomJoinRules, ""),
			room.CurrentState(spec.MRoomMember, bob.UserID),
			room.CurrentState(spec.MRoomMember, charlie),
		)
		authChain = append(authChain, eveJoinEvent)
		return
	}
	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/state_ids/{roomID}",
		srv.ValidFederationRequest(t, func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
			if pathParams["roomID"] != room.RoomID {
				t.Errorf("Received /state_ids for the wrong room: %s", room.RoomID)
				return util.JSONResponse{
					Code: 400,
					JSON: "wrong room",
				}
			}
			defer seenStateIDs.Finish()
			req, err := fr.HTTPRequest()
			must.NotError(t, "failed to get fed http reqest", err)
			atEventID := req.URL.Query().Get("event_id")
			t.Logf("received /state_ids at event %v (i=%v)", atEventID, eventIDToIndex[atEventID])
			authChain, stateEvents := getIncorrectState(atEventID)
			return util.JSONResponse{
				Code: 200,
				JSON: struct {
					AuthChainIDs []string `json:"auth_chain_ids"`
					PDUIDs       []string `json:"pdu_ids"`
				}{
					AuthChainIDs: asEventIDs(authChain),
					PDUIDs:       asEventIDs(stateEvents),
				},
			}
		}),
	)
	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/state/{roomID}",
		srv.ValidFederationRequest(t, func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
			if pathParams["roomID"] != room.RoomID {
				t.Errorf("Received /state for the wrong room: %s", room.RoomID)
				return util.JSONResponse{
					Code: 400,
					JSON: "wrong room",
				}
			}
			defer seenStateIDs.Finish()
			req, err := fr.HTTPRequest()
			must.NotError(t, "failed to get fed http reqest", err)
			atEventID := req.URL.Query().Get("event_id")
			t.Logf("received /state at event %v (i=%v)", atEventID, eventIDToIndex[atEventID])
			authChain, stateEvents := getIncorrectState(atEventID)
			return util.JSONResponse{
				Code: 200,
				JSON: struct {
					AuthChain gomatrixserverlib.EventJSONs `json:"auth_chain"`
					PDUs      gomatrixserverlib.EventJSONs `json:"pdus"`
				}{
					AuthChain: gomatrixserverlib.NewEventJSONsFromEvents(authChain),
					PDUs:      gomatrixserverlib.NewEventJSONsFromEvents(stateEvents),
				},
			}
		}),
	)

	// Send the last event to trigger /get_missing_events and /state_ids
	finalEvent := eveEvents[len(eveEvents)-1]
	srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{
		finalEvent.JSON(),
	}, nil)

	seenStateIDs.Waitf(t, 5*time.Second, "failed to see a /state{_ids} request")

	// wait until bob sees the final event
	alice.MustSyncUntil(t, client.SyncReq{Since: aliceSince}, client.SyncTimelineHasEventID(room.RoomID, finalEvent.EventID()))

	// the power levels should be the 3rd one (Alice:PL100, Bob:PL50, Charlie:PL59), not the 1st (Alice: PL100).
	// Servers which do not implement v2.1 will see the 1st one.
	content := alice.MustGetStateEventContent(t, room.RoomID, spec.MRoomPowerLevels, "")
	must.MatchGJSON(t, content, match.JSONKeyEqual("users."+client.GjsonEscape(alice.UserID), 100))
	// v2 Servers will fail here as bob does not exist in this map because it reset to an earlier event.
	must.MatchGJSON(t, content, match.JSONKeyEqual("users."+client.GjsonEscape(bob.UserID), 50))
	must.MatchGJSON(t, content, match.JSONKeyEqual("users."+client.GjsonEscape(charlie), 50))
}

func getMissingEventsHandler(t ct.TestLike, room *federation.ServerRoom, eventIDToIndex map[string]int, events []gomatrixserverlib.PDU) func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
	return func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
		if pathParams["roomID"] != room.RoomID {
			t.Errorf("Received /get_missing_events for the wrong room: %s", room.RoomID)
			return util.JSONResponse{
				Code: 400,
				JSON: "wrong room",
			}
		}
		latestEventID := gjson.ParseBytes(fr.Content()).Get("latest_events").Array()[0].Str
		j, ok := eventIDToIndex[latestEventID]
		if !ok {
			ct.Fatalf(t, "received /get_missing_events with latest=%s which is unknown", latestEventID)
		}
		t.Logf("received /get_missing_events with latest=%s (i=%d)", latestEventID, j)
		// return 10 earlier events.
		slice := events[j-10 : j]
		for _, ev := range slice {
			t.Logf("/get_missing_events returning %v (i=%v)", ev.EventID(), eventIDToIndex[ev.EventID()])
		}
		return util.JSONResponse{
			Code: 200,
			JSON: struct {
				Events gomatrixserverlib.EventJSONs `json:"events"`
			}{
				Events: gomatrixserverlib.NewEventJSONsFromEvents(slice),
			},
		}
	}
}

func asEventIDs(pdus []gomatrixserverlib.PDU) []string {
	eventIDs := make([]string, len(pdus))
	for i := range pdus {
		eventIDs[i] = pdus[i].EventID()
	}
	return eventIDs
}

func TestMSC4311FullCreateEventOnStrippedState(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // does not implement it yet
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "alice"})
	local := deployment.Register(t, "hs1", helpers.RegistrationOpts{LocalpartSuffix: "local"})
	remote := deployment.Register(t, "hs2", helpers.RegistrationOpts{LocalpartSuffix: "remote"})
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"room_version": roomVersion12,
		"preset":       "public_chat",
	})
	for _, target := range []*client.CSAPI{local, remote} {
		t.Logf("checking %s", target.UserID)
		alice.MustInviteRoom(t, roomID, target.UserID)
		resp, _ := target.MustSync(t, client.SyncReq{})
		inviteState := resp.Get(
			fmt.Sprintf("rooms.invite.%s.invite_state.events", client.GjsonEscape(roomID)),
		)
		must.NotEqual(t, len(inviteState.Array()), 0, "no events in invite_state")
		// find the create event
		found := false
		for _, ev := range inviteState.Array() {
			if ev.Get("type").Str == spec.MRoomCreate {
				found = true
				// we should have extra fields
				must.MatchGJSON(t, ev,
					match.JSONKeyPresent("origin_server_ts"),
				)
			}
		}
		if !found {
			ct.Errorf(t, "failed to find create event in invite_state")
		}
	}

}
