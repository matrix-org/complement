package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/gomatrix"
	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/matrix-org/gomatrixserverlib/spec"

	"github.com/matrix-org/gomatrixserverlib"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
)

// This tests that joining a room with ?server_name= works correctly.
// It does this by creating a room on the Complement server and joining HS1 to it.
// The Complement server then begins to refuse make/send_join requests and HS2 is
// asked to join the room ID with the ?server_name=HS1. We need to make sure that
// the HS is not just extracting the domain from the room ID and joining via that,
// hence the refusal for make/send_join on the Complement server.
// We can't use a bogus room ID domain either as auth checks on the
// m.room.create event would pick that up. We also can't tear down the Complement
// server because otherwise signing key lookups will fail.
func TestJoinViaRoomIDAndServerName(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	acceptMakeSendJoinRequests := true

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
	)
	srv.UnexpectedRequestsAreErrors = false // we will be sent transactions but that's okay
	cancel := srv.Listen()
	defer cancel()

	srv.Mux().Handle("/_matrix/federation/v1/make_join/{roomID}/{userID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !acceptMakeSendJoinRequests {
			w.WriteHeader(502)
			return
		}
		federation.MakeJoinRequestsHandler(srv, w, req)
	})).Methods("GET")
	srv.Mux().Handle("/_matrix/federation/v2/send_join/{roomID}/{eventID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !acceptMakeSendJoinRequests {
			w.WriteHeader(502)
			return
		}
		federation.SendJoinRequestsHandler(srv, w, req, false, false)
	})).Methods("PUT")

	ver := alice.GetDefaultRoomVersion(t)
	charlie := srv.UserID("charlie")
	serverRoom := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, charlie))

	// join the room by room ID, providing the serverName to join via
	alice.MustJoinRoom(t, serverRoom.RoomID, []spec.ServerName{srv.ServerName()})

	// remove the make/send join paths from the Complement server to force HS2 to join via HS1
	acceptMakeSendJoinRequests = false

	// join the room using ?server_name on HS2
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})
	roomID := bob.MustJoinRoom(t, serverRoom.RoomID, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
	})
	must.Equal(t, roomID, serverRoom.RoomID, "joined room mismatch")
}

// This tests that joining a room with multiple ?server_name=s works correctly.
// The join should succeed even if the first server is not in the room.
func TestJoinFederatedRoomFailOver(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})

	srv := federation.NewServer(t, deployment)
	cancel := srv.Listen()
	defer cancel()

	srv.Mux().Handle("/_matrix/federation/v1/make_join/{roomID}/{userID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		t.Logf("Complement homeserver responds to /make_join with 404, M_NOT_FOUND.")
		w.WriteHeader(404)
		w.Write([]byte(`{
			"errcode": "M_NOT_FOUND",
			"error": "Unknown room."
		}`))
	})).Methods("GET")

	roomID := bob.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	t.Logf("%s created room %s.", bob.UserID, roomID)

	t.Logf("%s joins the room via {complement,hs2}.", alice.UserID)
	alice.MustJoinRoom(t, roomID, []spec.ServerName{
		srv.ServerName(),
		deployment.GetFullyQualifiedHomeserverName(t, "hs2"),
	})
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
}

// This tests that joining a room over federation works in the presence of:
// - Events with missing signatures
// - Events with bad signatures
// - Events with correct signatures but the keys cannot be obtained
//
// None of these events will be critical to the integrity of the room: that
// is to say these events are not used as auth_events for the actual join -
// therefore the room should still be joinable.
//
// This test works by creating several federated rooms on Complement which have
// the properties listed above, then asking HS1 to join them and make sure that
// they 200 OK.
func TestJoinFederatedRoomWithUnverifiableEvents(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(nil, nil),
	)
	srv.UnexpectedRequestsAreErrors = false
	cancel := srv.Listen()
	defer cancel()

	ver := alice.GetDefaultRoomVersion(t)
	charlie := srv.UserID("charlie")

	// We explicitly do not run these in parallel in order to help debugging when these
	// tests fail. It doesn't appear to save us much time either!

	t.Run("/send_join response missing signatures shouldn't block room join", func(t *testing.T) {
		//t.Parallel()
		room := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, charlie))
		roomAlias := srv.MakeAliasMapping("MissingSignatures", room.RoomID)
		// create a normal event then remove the signatures key
		signedEvent := srv.MustCreateEvent(t, room, federation.Event{
			Sender:   charlie,
			StateKey: b.Ptr(""),
			Type:     "m.room.name",
			Content: map[string]interface{}{
				"name": "This event has no signature",
			},
		})
		raw := signedEvent.JSON()
		raw, err := sjson.SetRawBytes(raw, "signatures", []byte(`{}`))
		must.NotError(t, "failed to strip signatures key from event", err)
		verImpl, err := gomatrixserverlib.GetRoomVersion(room.Version)
		must.NotError(t, "failed to get room version", err)
		unsignedEvent, err := verImpl.NewEventFromTrustedJSON(raw, false)
		must.NotError(t, "failed to make Event from unsigned event JSON", err)
		room.AddEvent(unsignedEvent)
		alice.MustJoinRoom(t, roomAlias, nil)
	})
	t.Run("/send_join response with bad signatures shouldn't block room join", func(t *testing.T) {
		//t.Parallel()
		room := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, charlie))
		roomAlias := srv.MakeAliasMapping("BadSignatures", room.RoomID)
		// create a normal event then modify the signatures
		signedEvent := srv.MustCreateEvent(t, room, federation.Event{
			Sender:   charlie,
			StateKey: b.Ptr(""),
			Type:     "m.room.name",
			Content: map[string]interface{}{
				"name": "This event has a bad signature",
			},
		})
		newSignaturesBlock := map[string]interface{}{
			deployment.GetConfig().HostnameRunningComplement: map[string]string{
				string(srv.KeyID): "/3z+pJjiJXWhwfqIEzmNksvBHCoXTktK/y0rRuWJXw6i1+ygRG/suDCKhFuuz6gPapRmEMPVILi2mJqHHXPKAg",
			},
		}
		rawSig, err := json.Marshal(newSignaturesBlock)
		must.NotError(t, "failed to marshal bad signature block", err)
		raw := signedEvent.JSON()
		raw, err = sjson.SetRawBytes(raw, "signatures", rawSig)
		must.NotError(t, "failed to modify signatures key from event", err)
		verImpl, err := gomatrixserverlib.GetRoomVersion(room.Version)
		must.NotError(t, "failed to get room version", err)
		unsignedEvent, err := verImpl.NewEventFromTrustedJSON(raw, false)
		must.NotError(t, "failed to make Event from unsigned event JSON", err)
		room.AddEvent(unsignedEvent)
		alice.MustJoinRoom(t, roomAlias, nil)
	})
	t.Run("/send_join response with unobtainable keys shouldn't block room join", func(t *testing.T) {
		//t.Parallel()
		room := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, charlie))
		roomAlias := srv.MakeAliasMapping("UnobtainableKeys", room.RoomID)
		// create a normal event then modify the signatures to have a bogus key ID which Complement does
		// not have the keys for
		signedEvent := srv.MustCreateEvent(t, room, federation.Event{
			Sender:   charlie,
			StateKey: b.Ptr(""),
			Type:     "m.room.name",
			Content: map[string]interface{}{
				"name": "This event has an unobtainable key ID",
			},
		})
		newSignaturesBlock := map[string]interface{}{
			deployment.GetConfig().HostnameRunningComplement: map[string]string{
				string(srv.KeyID) + "bogus": "/3z+pJjiJXWhwfqIEzmNksvBHCoXTktK/y0rRuWJXw6i1+ygRG/suDCKhFuuz6gPapRmEMPVILi2mJqHHXPKAg",
			},
		}
		rawSig, err := json.Marshal(newSignaturesBlock)
		must.NotError(t, "failed to marshal bad signature block", err)
		raw := signedEvent.JSON()
		raw, err = sjson.SetRawBytes(raw, "signatures", rawSig)
		must.NotError(t, "failed to modify signatures key from event", err)
		verImpl, err := gomatrixserverlib.GetRoomVersion(room.Version)
		must.NotError(t, "failed to get room version", err)
		unsignedEvent, err := verImpl.NewEventFromTrustedJSON(raw, false)
		must.NotError(t, "failed to make Event from unsigned event JSON", err)
		room.AddEvent(unsignedEvent)
		alice.MustJoinRoom(t, roomAlias, nil)
	})
	t.Run("/send_join response with state with unverifiable auth events shouldn't block room join", func(t *testing.T) {
		// FIXME: https://github.com/matrix-org/dendrite/issues/2800
		//  (previously https://github.com/matrix-org/dendrite/issues/2028)
		runtime.SkipIf(t, runtime.Dendrite)

		room := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, charlie))
		roomAlias := srv.MakeAliasMapping("UnverifiableAuthEvents", room.RoomID)

		// create a normal event then modify the signatures
		rawEvent := srv.MustCreateEvent(t, room, federation.Event{
			Sender:   charlie,
			StateKey: &charlie,
			Type:     "m.room.member",
			Content: map[string]interface{}{
				"membership": "join",
				"name":       "This event has a bad signature",
			},
		}).JSON()
		rawSig, err := json.Marshal(map[string]interface{}{
			deployment.GetConfig().HostnameRunningComplement: map[string]string{
				string(srv.KeyID): "/3z+pJjiJXWhwfqIEzmNksvBHCoXTktK/y0rRuWJXw6i1+ygRG/suDCKhFuuz6gPapRmEMPVILi2mJqHHXPKAg",
			},
		})
		must.NotError(t, "failed to marshal bad signature block", err)
		rawEvent, err = sjson.SetRawBytes(rawEvent, "signatures", rawSig)
		must.NotError(t, "failed to modify signatures key from event", err)
		verImpl, err := gomatrixserverlib.GetRoomVersion(room.Version)
		must.NotError(t, "failed to get room version", err)
		badlySignedEvent, err := verImpl.NewEventFromTrustedJSON(rawEvent, false)
		must.NotError(t, "failed to make Event from badly signed event JSON", err)
		room.AddEvent(badlySignedEvent)
		t.Logf("Created badly signed auth event %s", badlySignedEvent.EventID())

		// and now add another event which will use it as an auth event.
		goodEvent := srv.MustCreateEvent(t, room, federation.Event{
			Sender:   charlie,
			StateKey: &charlie,
			Type:     "m.room.member",
			Content: map[string]interface{}{
				"membership": "leave",
			},
		})
		// double-check that the bad event is in its auth events
		containsEvent := false
		for _, authEventID := range goodEvent.AuthEventIDs() {
			if authEventID == badlySignedEvent.EventID() {
				containsEvent = true
				break
			}
		}
		if !containsEvent {
			t.Fatalf("Bad event didn't appear in auth events of state event")
		}
		room.AddEvent(goodEvent)
		t.Logf("Created state event %s", goodEvent.EventID())

		alice.MustJoinRoom(t, roomAlias, nil)
	})
}

// This test checks that users cannot circumvent the auth checks via send_join.
func TestBannedUserCannotSendJoin(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleTransactionRequests(nil, nil),
	)
	cancel := srv.Listen()
	origin := srv.ServerName()
	defer cancel()

	fedClient := srv.FederationClient(deployment)

	charlie := srv.UserID("charlie")

	// alice creates a room, and bans charlie from it.
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	alice.SendEventSynced(t, roomID, b.Event{
		Type:     "m.room.member",
		Sender:   alice.UserID,
		StateKey: &charlie,
		Content: map[string]interface{}{
			"membership": "ban",
		},
	})

	// charlie sends a make_join for a different user
	makeJoinResp, err := fedClient.MakeJoin(context.Background(), origin, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), roomID, srv.UserID("charlie2"))
	must.NotError(t, "MakeJoin", err)

	// ... and does a switcheroo to turn it into a join for himself
	makeJoinResp.JoinEvent.SenderID = charlie
	makeJoinResp.JoinEvent.StateKey = &charlie

	verImpl, err := gomatrixserverlib.GetRoomVersion(makeJoinResp.RoomVersion)
	must.NotError(t, "JoinEvent.GetRoomVersion", err)
	eb := verImpl.NewEventBuilderFromProtoEvent(&makeJoinResp.JoinEvent)
	joinEvent, err := eb.Build(time.Now(), srv.ServerName(), srv.KeyID, srv.Priv)
	must.NotError(t, "JoinEvent.Build", err)

	// SendJoin should return a 403.
	_, err = fedClient.SendJoin(context.Background(), origin, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), joinEvent)
	if err == nil {
		t.Errorf("SendJoin returned 200, want 403")
	} else if httpError, ok := err.(gomatrix.HTTPError); ok {
		t.Logf("SendJoin => %d/%s", httpError.Code, string(httpError.Contents))
		if httpError.Code != 403 {
			t.Errorf("expected 403, got %d", httpError.Code)
		}
		must.MatchJSONBytes(t, httpError.Contents, match.JSONKeyEqual("errcode", "M_FORBIDDEN"))
	} else {
		t.Errorf("SendJoin: non-HTTPError: %v", err)
	}

	// Alice checks the room state to check that charlie isn't a member
	content := alice.MustGetStateEventContent(t, roomID, "m.room.member", charlie)
	must.MatchGJSON(t, content,
		match.JSONKeyEqual("membership", "ban"),
	)
}

// This test checks that we cannot submit anything via /v1/send_join except a join.
func TestCannotSendNonJoinViaSendJoinV1(t *testing.T) {
	testValidationForSendMembershipEndpoint(t, "/_matrix/federation/v1/send_join", "join", nil)
}

// This test checks that we cannot submit anything via /v2/send_join except a join.
func TestCannotSendNonJoinViaSendJoinV2(t *testing.T) {
	testValidationForSendMembershipEndpoint(t, "/_matrix/federation/v2/send_join", "join", nil)
}

// This test checks that we cannot submit anything via /v1/send_leave except a leave.
func TestCannotSendNonLeaveViaSendLeaveV1(t *testing.T) {
	testValidationForSendMembershipEndpoint(t, "/_matrix/federation/v1/send_leave", "leave", nil)
}

// This test checks that we cannot submit anything via /v2/send_leave except a leave.
func TestCannotSendNonLeaveViaSendLeaveV2(t *testing.T) {
	testValidationForSendMembershipEndpoint(t, "/_matrix/federation/v2/send_leave", "leave", nil)
}

// testValidationForSendMembershipEndpoint attempts to submit a range of events via the given endpoint
// and checks that they are all rejected.
func testValidationForSendMembershipEndpoint(t *testing.T, baseApiPath, expectedMembership string, createRoomOpts map[string]interface{}) {
	if createRoomOpts == nil {
		createRoomOpts = map[string]interface{}{
			"preset": "public_chat",
		}
	}

	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleTransactionRequests(nil, nil),
	)
	cancel := srv.Listen()
	defer cancel()

	// alice creates a room, and charlie joins it
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	roomId := alice.MustCreateRoom(t, createRoomOpts)
	charlie := srv.UserID("charlie")
	room := srv.MustJoinRoom(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), roomId, charlie)

	// a helper function which makes a send_* request to the given path and checks
	// that it fails with a 400 error
	assertRequestFails := func(t *testing.T, event gomatrixserverlib.PDU) {
		path := fmt.Sprintf("%s/%s/%s",
			baseApiPath,
			url.PathEscape(event.RoomID().String()),
			url.PathEscape(event.EventID()),
		)
		t.Logf("PUT %s", path)
		req := fclient.NewFederationRequest("PUT", srv.ServerName(), deployment.GetFullyQualifiedHomeserverName(t, "hs1"), path)
		if err := req.SetContent(event); err != nil {
			t.Errorf("req.SetContent: %v", err)
			return
		}

		var res interface{}

		err := srv.SendFederationRequest(context.Background(), t, deployment, req, &res)

		if err == nil {
			t.Errorf("send request returned 200")
			return
		}

		httpError, ok := err.(gomatrix.HTTPError)
		if !ok {
			t.Errorf("not an HTTPError: %v", err)
			return
		}

		t.Logf("%s returned %d/%s", baseApiPath, httpError.Code, string(httpError.Contents))
		if httpError.Code != 400 {
			t.Errorf("expected 400, got %d", httpError.Code)
		}
	}

	t.Run("regular event", func(t *testing.T) {
		event := srv.MustCreateEvent(t, room, federation.Event{
			Type:    "m.room.message",
			Sender:  charlie,
			Content: map[string]interface{}{"body": "bzz"},
		})
		assertRequestFails(t, event)
	})
	t.Run("non-state membership event", func(t *testing.T) {
		event := srv.MustCreateEvent(t, room, federation.Event{
			Type:    "m.room.member",
			Sender:  charlie,
			Content: map[string]interface{}{"body": "bzz"},
		})
		assertRequestFails(t, event)
	})

	// try membership events of various types, other than that expected by
	// the endpoint
	for _, membershipType := range []string{"join", "leave", "knock", "invite"} {
		if membershipType == expectedMembership {
			continue
		}
		event := srv.MustCreateEvent(t, room, federation.Event{
			Type:     "m.room.member",
			Sender:   charlie,
			StateKey: &charlie,
			Content:  map[string]interface{}{"membership": membershipType},
		})
		t.Run(membershipType+" event", func(t *testing.T) {
			assertRequestFails(t, event)
		})
	}

	// right sort of membership, but mismatched state_key
	t.Run("event with mismatched state key", func(t *testing.T) {
		event := srv.MustCreateEvent(t, room, federation.Event{
			Type:     "m.room.member",
			Sender:   charlie,
			StateKey: b.Ptr(srv.UserID("doris")),
			Content:  map[string]interface{}{"membership": expectedMembership},
		})
		assertRequestFails(t, event)
	})
}

// Tests an implementation's support for MSC3706-style partial-state responses to send_join.
//
// Will be skipped if the server returns a full-state response.
func TestSendJoinPartialStateResponse(t *testing.T) {
	// start with a homeserver with two users
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),

		// accept incoming presence transactions, etc
		federation.HandleTransactionRequests(nil, nil),
	)
	cancel := srv.Listen()
	defer cancel()
	origin := srv.ServerName()

	// annoyingly we can't get to the room that alice and bob already share (see https://github.com/matrix-org/complement/issues/254)
	// so we have to create a new one.
	// alice creates a room, which bob joins
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.MustJoinRoom(t, roomID, nil)

	// now we send a make_join...
	charlie := srv.UserID("charlie")
	fedClient := srv.FederationClient(deployment)
	makeJoinResp, err := fedClient.MakeJoin(context.Background(), origin, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), roomID, charlie)
	if err != nil {
		t.Fatalf("make_join failed: %v", err)
	}

	// ... construct a signed join event ...
	verImpl, err := gomatrixserverlib.GetRoomVersion(makeJoinResp.RoomVersion)
	must.NotError(t, "JoinEvent.GetRoomVersion", err)
	eb := verImpl.NewEventBuilderFromProtoEvent(&makeJoinResp.JoinEvent)
	joinEvent, err := eb.Build(time.Now(), srv.ServerName(), srv.KeyID, srv.Priv)
	if err != nil {
		t.Fatalf("failed to sign join event: %v", err)
	}

	// and send_join it, with the magic param
	sendJoinResp, err := fedClient.SendJoinPartialState(context.Background(), origin, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), joinEvent)
	if err != nil {
		t.Fatalf("send_join failed: %v", err)
	}

	if !sendJoinResp.MembersOmitted {
		t.Skip("Server does not support partial_state")
	}

	// check the returned state events match those expected
	var returnedStateEventKeys []interface{}
	for _, ev := range sendJoinResp.StateEvents {
		returnedStateEventKeys = append(returnedStateEventKeys, typeAndStateKeyForEvent(gjson.ParseBytes(ev)))
	}
	must.CheckOffAll(t, returnedStateEventKeys, []interface{}{
		"m.room.create|",
		"m.room.power_levels|",
		"m.room.join_rules|",
		"m.room.history_visibility|",
		// Expect Alice and Bob's membership here because they're room heroes
		"m.room.member|" + alice.UserID,
		"m.room.member|" + bob.UserID,
	})

	// check the returned auth events match those expected.
	// Now that we include heroes in the partial join response,
	// all of the events are included under "state" and so we don't expect any
	// extra auth_events.
	// TODO: add in a second e.g. power_levels event so that we add stuff to the
	// auth chain.
	var returnedAuthEventKeys []interface{}
	for _, ev := range sendJoinResp.AuthEvents {
		returnedAuthEventKeys = append(returnedAuthEventKeys, typeAndStateKeyForEvent(gjson.ParseBytes(ev)))
	}
	must.CheckOffAll(t, returnedAuthEventKeys, []interface{}{})

	// check the server list. Only one, so we can use HaveInOrder even though the list is unordered
	must.HaveInOrder(t, sendJoinResp.ServersInRoom, []string{"hs1"})
}

// This test verifies that events sent into a room between a /make_join and
// /send_join are not lost to the joining server. When an event is created
// during the join handshake, the join event's prev_events (set at make_join
// time) won't reference it, creating two forward extremities. The server
// handling the join should ensure the joining server can discover the missed
// event, for example by sending a follow-up event that references both
// extremities, prompting the joining server to backfill.
//
// See https://github.com/element-hq/synapse/pull/19390
func TestEventBetweenMakeJoinAndSendJoinIsNotLost(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	// We track the message event ID sent between make_join and send_join.
	// After send_join, we wait for hs1 to send us either:
	//  - the message event itself, or
	//  - any event whose prev_events reference the message (e.g. a dummy event)
    //
	// atomic.Value is used because messageEventID is written on the main goroutine and
	// read on the HTTP handler goroutine, and needs synchronization (without
	// synchronization, writes are not guaranteed to be observed by other goroutines)
	var messageEventID atomic.Value
	messageDiscoverableWaiter := helpers.NewWaiter()

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
	)
	// After send_join, hs1 will start sending us federation transactions via
	// /_matrix/federation/v1/send/{txnID}. Since we handle /send manually
	// below, any other requests (e.g. key fetches) that arrive unexpectedly
	// should be tolerated rather than treated as test failures.
	srv.UnexpectedRequestsAreErrors = false

	// Custom /send handler: hs1 will push new room events to us via federation
	// transactions once we've joined. We use a raw handler because the
	// Complement server is not fully in the room until send_join completes, so
	// we can't use HandleTransactionRequests (which requires the room in
	// srv.rooms). Instead we parse the raw transaction body ourselves.
	srv.Mux().Handle("/_matrix/federation/v1/send/{transactionID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		must.NotError(t, "failed to read request body in /send handler: %v", err)
		txn := gjson.ParseBytes(body)
		txn.Get("pdus").ForEach(func(_, pdu gjson.Result) bool {
			eventID := pdu.Get("event_id").String()
			eventType := pdu.Get("type").String()
			t.Logf("Received PDU via /send: type=%s id=%s", eventType, eventID)

			// messageEventID is set after make_join but before send_join.
			// Transactions can arrive before that window, so skip PDUs that
			// arrive before we know which event to look for.
			msgID, _ := messageEventID.Load().(string)
			if msgID == "" {
				return true
			}

			// Check if this IS the message event (server pushed it directly).
			if eventID == msgID {
				messageDiscoverableWaiter.Finish()
				return true
			}

			// Check if this event's prev_events directly reference the message (e.g. a dummy
			// event tying the two forward extremities together). If so, the joining server
			// can backfill from that event and will discover the message.
			//
			// XXX: We only check one level of prev_events: if the reference is deeper in the
			// DAG, it's valid and the joining server can still reach the message through
			// backfill but our checks don't account for that yet (feel free to edit this
			// assertion if you run into this)
			pdu.Get("prev_events").ForEach(func(_, prevEvent gjson.Result) bool {
				if prevEvent.String() == msgID {
					messageDiscoverableWaiter.Finish()
					return false
				}
				return true
			})

			return true
		})
		w.WriteHeader(200)
		// Respond with an empty PDU error map, which is the federation /send
		// success response format: each key would be a PDU ID whose processing
		// failed; an empty object means all PDUs were accepted.
		w.Write([]byte(`{"pdus":{}}`))
	})).Methods("PUT")

	cancel := srv.Listen()
	defer cancel()

	// Alice creates a room on hs1.
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	charlie := srv.UserID("charlie")
	origin := srv.ServerName()
	fedClient := srv.FederationClient(deployment)

	// Step 1: make_join, hs1 returns a join event template whose prev_events
	// reflect the current room DAG tips.
	makeJoinResp, err := fedClient.MakeJoin(
		context.Background(), origin,
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
		roomID, charlie,
	)
	must.NotError(t, "MakeJoin", err)

	// Step 2: Alice sends a message on hs1. This advances the DAG past the
	// point captured by make_join's prev_events. The Complement server is not
	// yet in the room, so it won't receive this event via normal federation.
	messageEventID.Store(alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Message sent between make_join and send_join",
		},
	}))
	t.Logf("Alice sent message %s between make_join and send_join", messageEventID.Load())

	// Step 3: Build and sign the join event, then send_join.
	// The join event's prev_events are from step 1 (before the message),
	// so persisting it on hs1 creates two forward extremities: the message
	// and the join.
	verImpl, err := gomatrixserverlib.GetRoomVersion(makeJoinResp.RoomVersion)
	must.NotError(t, "GetRoomVersion", err)
	eb := verImpl.NewEventBuilderFromProtoEvent(&makeJoinResp.JoinEvent)
	joinEvent, err := eb.Build(time.Now(), srv.ServerName(), srv.KeyID, srv.Priv)
	must.NotError(t, "Build join event", err)

	_, err = fedClient.SendJoin(
		context.Background(), origin,
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
		joinEvent,
	)
	must.NotError(t, "SendJoin", err)

	// Step 4: hs1 should make the missed message discoverable to the joining
	// server. We accept either receiving the message event directly, or
	// receiving any event whose prev_events reference it (allowing the
	// joining server to backfill).
	messageDiscoverableWaiter.Waitf(t, 5*time.Second,
		"Timed out waiting for message event %s to become discoverable — "+
			"the event sent between make_join and send_join was lost to the "+
			"joining server", messageEventID.Load(),
	)
}

// given an event JSON, return the type and state_key, joined with a "|"
func typeAndStateKeyForEvent(result gjson.Result) string {
	return strings.Join([]string{result.Map()["type"].Str, result.Map()["state_key"].Str}, "|")
}

func TestJoinFederatedRoomFromApplicationServiceBridgeUser(t *testing.T) {
	// Dendrite doesn't read AS registration files from Complement yet
	runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/complement/issues/514

	deployment := complement.OldDeploy(t, b.BlueprintHSWithApplicationService)
	defer deployment.Destroy(t)

	// Create the application service bridge user to try to join the room from
	asUserID := "@the-bridge-user:hs1"
	as := deployment.AppServiceUser(t, "hs1", asUserID)

	// Create the federated remote user which will create the room
	remoteCharlie := deployment.Register(t, "hs2", helpers.RegistrationOpts{})

	t.Run("join remote federated room as application service user", func(t *testing.T) {
		//t.Parallel()
		// Create the room from a remote homeserver
		roomID := remoteCharlie.MustCreateRoom(t, map[string]interface{}{
			"preset": "public_chat",
			"name":   "hs2 room",
		})

		// Join the AS bridge user to the remote federated room (without a profile set)
		as.MustJoinRoom(t, roomID, []spec.ServerName{
			deployment.GetFullyQualifiedHomeserverName(t, "hs2"),
		})
	})
}
