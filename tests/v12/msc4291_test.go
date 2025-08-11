package tests

import (
	"fmt"
	"net/url"
	"slices"
	"testing"

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
