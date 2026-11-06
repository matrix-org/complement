package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/matrix-org/util"
	"github.com/tidwall/gjson"
)

// INV: Out-of-band invite tests.
//
//	An out-of-band (OOB) invite is an invite sent to a server which is not in the room. It is the one
//	case where a server legitimately cannot fill in the state DAG for an event it accepts: it cannot
//	walk back to the create event via /get_missing_events because it isn't participating in the room.
//	This is true both when the server has never been in the room and when it was in the room
//	previously, in which case it has a *stale* state DAG which does not reach the invite event.
//
//	INV00(IO): an OOB invite is accepted by the invited server and can then be refused by the invited
//	           user. The leave event must be made via /make_leave as the invited server cannot
//	           calculate prev_state_events itself.
//	  A: the invited server has no state for the room.
//	  B: the invited server has state for the room from a previous join.
//	INV01(IO): an OOB invite can be rescinded by the inviting server sending a leave event over /send.
//	  The rescission names the invite in its prev_state_events: the invited server cannot calculate
//	  the auth events for the leave event, so that reference is the only thing tying the rescission
//	  to the invite it rescinds.
//	  A: the invited server has no state for the room.
//	  B: the invited server has state for the room from a previous join.
//	INV02-Inbound: a rescission which does not name the invite in its prev_state_events is ignored.

// out-of-band invite test cases, shared between the inbound and outbound tests.
var oobInviteTestCases = []struct {
	testCodeSuffix string
	name           string
	// if true, the invited server joins and then leaves the room before the invite is sent, so it
	// has a stale state DAG for the room rather than no state at all.
	withPriorState bool
	// if true the invite is rescinded by the inviting server, else it is refused by the invited user.
	rescind bool
}{
	{
		testCodeSuffix: "00A",
		name:           "the invited user can refuse an out-of-band invite",
	},
	{
		testCodeSuffix: "00B",
		name:           "the invited user can refuse an out-of-band invite when their server has state from a previous join",
		withPriorState: true,
	},
	{
		testCodeSuffix: "01A",
		name:           "the inviting server can rescind an out-of-band invite",
		rescind:        true,
	},
	{
		testCodeSuffix: "01B",
		name:           "the inviting server can rescind an out-of-band invite when the invited server has state from a previous join",
		withPriorState: true,
		rescind:        true,
	},
}

// INV00-Inbound, INV01-Inbound: Complement invites a user on the homeserver to a room the
// homeserver is not in, then the invite is either refused by the invited user or rescinded by us.
func TestMSC4242OutOfBandInviteInbound(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	hs1 := deployment.GetFullyQualifiedHomeserverName(t, "hs1")

	for _, tc := range oobInviteTestCases {
		t.Run("INV"+tc.testCodeSuffix+"-Inbound", func(t *testing.T) {
			t.Logf("INV%s-Inbound: %s", tc.testCodeSuffix, tc.name)
			alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
			charlie := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
			charlieLeft := helpers.NewWaiter()

			srv := federation.NewServer(t, deployment,
				federation.HandleKeyRequests(),
				federation.HandleTransactionRequests(func(ev gomatrixserverlib.PDU) {
					if isMembershipEvent(ev, charlie.UserID, spec.Leave) {
						charlieLeft.Finish()
					}
				}, nil),
				federation.HandleEventRequests(),
				federation.HandleMakeSendJoinRequests(),
			)
			// The homeserver has no way of filling in the state DAG for an out-of-band invite, but
			// it is allowed to try, so don't fail the test on requests we don't handle.
			srv.UnexpectedRequestsAreErrors = false
			cancel := srv.Listen()
			defer cancel()
			bob := srv.UserID("bob")

			room := srv.MustMakeRoom(t, roomVersion,
				federation.InitialRoomEvents(roomVersion, bob),
				federation.WithImpl(ServerRoomImplStateDAG(t, srv)),
			)
			serveGetMissingEvents(t, srv, room)
			var makeLeaveProto *gomatrixserverlib.ProtoEvent
			var leaveEvent gomatrixserverlib.PDU
			gotSendLeave := helpers.NewWaiter()
			handleMakeSendLeaveRequests(t, srv, room,
				func(proto *gomatrixserverlib.ProtoEvent) { makeLeaveProto = proto },
				func(ev gomatrixserverlib.PDU) {
					leaveEvent = ev
					gotSendLeave.Finish()
				},
			)

			if tc.withPriorState {
				// Charlie joins then leaves, so hs1 has the state DAG for this room up to Charlie's
				// leave, but is no longer in the room.
				charlie.MustJoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})
				charlie.MustLeaveRoom(t, room.RoomID)
				charlieLeft.Waitf(t, 5*time.Second, "did not receive Charlie's leave event")
				// Move the state DAG on without telling hs1, so the invite we send references state
				// events hs1 has never seen and cannot get without being in the room.
				unknown := generateDisplayNameChanges(t, srv, room, bob, 3, true)
				t.Logf("INV%s-Inbound: hs1 will not know about %v", tc.testCodeSuffix, AsEventIDs(t, unknown))
			}

			inviteEvent := mustSendOutOfBandInvite(t, srv, deployment, hs1, room, bob, alice.UserID)
			// Remember where we are in the sync stream: a user leaving a room they were only
			// invited to is visible in an incremental sync, but not in an initial one.
			since := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, room.RoomID))

			if tc.rescind {
				// A state event between the invite and the rescission, so the invite is no longer
				// the forward extremity of the state DAG and has to be named explicitly.
				roomName := srv.MustCreateEvent(t, room, federation.Event{
					Type:     spec.MRoomName,
					StateKey: &empty,
					Sender:   bob,
					Content: map[string]interface{}{
						"name": "rescinding",
					},
				})
				room.AddEvent(roomName)

				// Bob rescinds the invite. hs1 is not in the room so pushing the leave event to it
				// is the only way it can find out. hs1 cannot calculate the auth events for the
				// leave event, so the rescission names the invite in its prev_state_events.
				rescindEvent := mustCreateEvent(t, srv, room, MSC4242Event{
					Event: federation.Event{
						Type:     spec.MRoomMember,
						StateKey: &alice.UserID,
						Sender:   bob,
						Content: map[string]interface{}{
							"membership": spec.Leave,
							"reason":     "rescinding the invite",
						},
					},
					PrevStateEvents: []string{roomName.EventID(), inviteEvent.EventID()},
				})
				room.AddEvent(rescindEvent)
				srv.MustSendTransaction(t, deployment, hs1, AsEventJSONs([]gomatrixserverlib.PDU{rescindEvent}), nil)
			} else {
				// Alice refuses the invite. hs1 cannot calculate prev_state_events for the leave
				// event as it does not have the state DAG, so it must ask us via /make_leave.
				alice.MustLeaveRoom(t, room.RoomID)
				gotSendLeave.Waitf(t, 5*time.Second, "homeserver did not send us a leave event via /send_leave")
				if makeLeaveProto == nil {
					ct.Fatalf(t, "homeserver did not call /make_leave when refusing an out-of-band invite")
				}
				if makeLeaveProto.PrevStateEvents == nil {
					ct.Fatalf(t, "/make_leave template had no prev_state_events")
				}
				// The homeserver must use the prev_state_events we gave it in the /make_leave
				// template, which point at the invite event as the latest state event in the room.
				if !slices.Equal(leaveEvent.PrevStateEventIDs(), *makeLeaveProto.PrevStateEvents) {
					ct.Errorf(
						t, "leave event prev_state_events %v does not match the /make_leave template %v",
						leaveEvent.PrevStateEventIDs(), *makeLeaveProto.PrevStateEvents,
					)
				}
				must.MatchJSONBytes(t, leaveEvent.JSON(),
					match.JSONKeyMissing("auth_events"),
					prevStateEventsMatcher("prev_state_events", []string{inviteEvent.EventID()}),
				)
			}

			alice.MustSyncUntil(t, client.SyncReq{Since: since}, client.SyncLeftFrom(alice.UserID, room.RoomID))
		})
	}
}

// INV00-Outbound, INV01-Outbound: the homeserver invites a Complement user to a room Complement is
// not in, then the invite is either refused by us or rescinded by the homeserver.
func TestMSC4242OutOfBandInviteOutbound(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	hs1 := deployment.GetFullyQualifiedHomeserverName(t, "hs1")

	for _, tc := range oobInviteTestCases {
		t.Run("INV"+tc.testCodeSuffix+"-Outbound", func(t *testing.T) {
			t.Logf("INV%s-Outbound: %s", tc.testCodeSuffix, tc.name)
			alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
			// bob is the invited user, dave is only used to give Complement state for the room.
			// They are assigned after Listen(), but the callbacks below only run after that.
			var bob, dave string
			var inviteEvent, rescindEvent gomatrixserverlib.PDU
			gotInvite := helpers.NewWaiter()
			gotRescind := helpers.NewWaiter()

			srv := federation.NewServer(t, deployment,
				federation.HandleKeyRequests(),
				federation.HandleEventRequests(),
				federation.HandleInviteRequests(func(ev gomatrixserverlib.PDU) {
					if !isMembershipEvent(ev, bob, spec.Invite) {
						return // e.g dave's invite when setting up prior state
					}
					inviteEvent = ev
					gotInvite.Finish()
				}),
			)
			srv.UnexpectedRequestsAreErrors = false
			// We are not in the room, so the built-in transaction handler would drop the leave event
			// the homeserver sends us when the invite is rescinded.
			handleTransactionsForUnknownRooms(t, srv, func(ev gomatrixserverlib.PDU) {
				if isMembershipEvent(ev, bob, spec.Leave) {
					rescindEvent = ev
					gotRescind.Finish()
				}
			})
			cancel := srv.Listen()
			defer cancel()
			// Listen() chooses the port, which is part of our server name, so our user IDs are only
			// stable once it has been called.
			bob = srv.UserID("bob")
			dave = srv.UserID("dave")

			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"room_version": roomVersion,
				"preset":       "private_chat",
			})
			if tc.withPriorState {
				// Dave joins then leaves, so Complement has the state DAG for this room up to
				// Dave's leave, but is no longer in the room.
				alice.MustInviteRoom(t, roomID, dave)
				MustJoinRoom(t, srv, deployment, hs1, roomID, dave)
				daveJoined := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(dave, roomID))
				srv.MustLeaveRoom(t, deployment, hs1, roomID, dave)
				alice.MustSyncUntil(t, client.SyncReq{Since: daveJoined}, client.SyncLeftFrom(dave, roomID))
			}
			// Move the state DAG on so the invite references state events Complement does not have.
			changeDisplayName(t, alice, roomID, "alice", 2)

			// Remember where we are in the sync stream: Bob leaving a room he was only invited to is
			// visible in an incremental sync, but not in an initial one.
			since := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
			alice.MustInviteRoom(t, roomID, bob)
			gotInvite.Waitf(t, 5*time.Second, "did not receive an invite over federation")
			// The invite must carry prev_state_events like any other state event, even though we
			// have no way of checking that they reach the create event.
			must.MatchJSONBytes(t, inviteEvent.JSON(),
				match.JSONKeyMissing("auth_events"),
				match.JSONKeyPresent("prev_state_events"),
			)
			must.NotEqual(t, len(inviteEvent.PrevStateEventIDs()), 0, "invite event had no prev_state_events")

			if tc.rescind {
				// Alice, the inviter, rescinds the invite. We are not in the room, so this is the
				// only way we can find out.
				alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "kick"},
					client.WithJSONBody(t, map[string]interface{}{
						"user_id": bob,
						"reason":  "rescinding the invite",
					}),
				)
				gotRescind.Waitf(t, 5*time.Second, "did not receive the rescinded invite over federation")
				// the rescind must be pinned to the invite, which is the latest state event in the room.
				must.MatchJSONBytes(t, rescindEvent.JSON(),
					prevStateEventsMatcher("prev_state_events", []string{inviteEvent.EventID()}),
				)
			} else {
				// We refuse the invite. We cannot calculate prev_state_events for the leave event as
				// we do not have the state DAG, so the homeserver must give them to us in /make_leave.
				protoLeave, leaveEvent := mustRefuseInvite(t, srv, deployment, hs1, roomID, bob)
				if protoLeave.PrevStateEvents == nil {
					ct.Fatalf(t, "/make_leave template had no prev_state_events")
				}
				if !slices.Contains(*protoLeave.PrevStateEvents, inviteEvent.EventID()) {
					ct.Errorf(
						t, "/make_leave template prev_state_events %v does not include the invite event %s",
						*protoLeave.PrevStateEvents, inviteEvent.EventID(),
					)
				}
				must.MatchJSONBytes(t, leaveEvent.JSON(), match.JSONKeyMissing("auth_events"))
			}

			alice.MustSyncUntil(t, client.SyncReq{Since: since}, client.SyncLeftFrom(bob, roomID))
		})
	}
}

func isMembershipEvent(ev gomatrixserverlib.PDU, userID, membership string) bool {
	return ev.Type() == spec.MRoomMember && ev.StateKey() != nil && *ev.StateKey() == userID &&
		gjson.GetBytes(ev.Content(), "membership").Str == membership
}

// mustSendOutOfBandInvite invites userID to a Complement room via /_matrix/federation/v2/invite.
// The invited server is not in the room so it cannot fill in the state DAG for this event.
func mustSendOutOfBandInvite(
	t *testing.T, srv *federation.Server, deployment federation.FederationDeployment,
	destination spec.ServerName, room *federation.ServerRoom, sender, userID string,
) gomatrixserverlib.PDU {
	t.Helper()
	inviteEvent := srv.MustCreateEvent(t, room, federation.Event{
		Type:     spec.MRoomMember,
		StateKey: &userID,
		Sender:   sender,
		Content: map[string]interface{}{
			"membership": spec.Invite,
		},
	})
	room.AddEvent(inviteEvent)

	var strippedState []gomatrixserverlib.InviteStrippedState
	for _, ev := range room.AllCurrentState() {
		switch ev.Type() {
		case spec.MRoomCreate, spec.MRoomJoinRules, spec.MRoomName:
			strippedState = append(strippedState, gomatrixserverlib.NewInviteStrippedState(ev))
		case spec.MRoomMember:
			if ev.StateKey() != nil && *ev.StateKey() == sender {
				strippedState = append(strippedState, gomatrixserverlib.NewInviteStrippedState(ev))
			}
		}
	}
	inviteReq, err := fclient.NewInviteV2Request(inviteEvent, strippedState)
	must.NotError(t, "failed to make the invite request", err)
	_, err = srv.FederationClient(deployment).SendInviteV2(
		context.Background(), spec.ServerName(srv.ServerName()), destination, inviteReq,
	)
	must.NotError(t, "failed to send /invite", err)
	t.Logf("sent out-of-band invite %s for %s", inviteEvent.EventID(), userID)
	return inviteEvent
}

// mustRefuseInvite rejects an invite to a room Complement is not in by doing the /make_leave,
// /send_leave dance. Unlike federation.Server.MustLeaveRoom this always goes via /make_leave, which
// is what a server has to do when it cannot calculate the prev_state_events itself.
func mustRefuseInvite(
	t *testing.T, srv *federation.Server, deployment federation.FederationDeployment,
	remoteServer spec.ServerName, roomID, userID string,
) (gomatrixserverlib.ProtoEvent, gomatrixserverlib.PDU) {
	t.Helper()
	origin := spec.ServerName(srv.ServerName())
	fedClient := srv.FederationClient(deployment)
	makeLeaveResp, err := fedClient.MakeLeave(context.Background(), origin, remoteServer, roomID, userID)
	must.NotError(t, "make_leave failed", err)
	verImpl, err := gomatrixserverlib.GetRoomVersion(makeLeaveResp.RoomVersion)
	must.NotError(t, "make_leave returned an unknown room version", err)
	eb := verImpl.NewEventBuilderFromProtoEvent(&makeLeaveResp.LeaveEvent)
	leaveEvent, err := eb.Build(time.Now(), origin, srv.KeyID, srv.Priv)
	must.NotError(t, "failed to build the leave event", err)
	must.NotError(t, "send_leave failed", fedClient.SendLeave(context.Background(), origin, remoteServer, leaveEvent))
	return makeLeaveResp.LeaveEvent, leaveEvent
}

// handleMakeSendLeaveRequests handles /make_leave and /send_leave for the given room. Complement has
// no built-in handlers for these. The leave template is made by the room's ServerRoomImpl so it has
// prev_state_events set for state DAG rooms.
func handleMakeSendLeaveRequests(
	t *testing.T, srv *federation.Server, room *federation.ServerRoom,
	onMakeLeave func(proto *gomatrixserverlib.ProtoEvent), onSendLeave func(ev gomatrixserverlib.PDU),
) {
	wrongRoom := func(roomID string) util.JSONResponse {
		ct.Errorf(t, "received a leave request for the wrong room: %s != %s", roomID, room.RoomID)
		return util.JSONResponse{
			Code: 404,
			JSON: map[string]string{"error": "complement: unknown room " + roomID},
		}
	}
	srv.Mux().HandleFunc("/_matrix/federation/v1/make_leave/{roomID}/{userID}",
		srv.ValidFederationRequest(t, func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
			if pathParams["roomID"] != room.RoomID {
				return wrongRoom(pathParams["roomID"])
			}
			userID := pathParams["userID"]
			proto, err := room.ProtoEventCreator(room, federation.Event{
				Type:     spec.MRoomMember,
				StateKey: &userID,
				Sender:   userID,
				Content: map[string]interface{}{
					"membership": spec.Leave,
				},
			})
			if err != nil {
				ct.Errorf(t, "failed to make the leave event template: %s", err)
				return util.JSONResponse{
					Code: 500,
					JSON: map[string]string{"error": "complement: " + err.Error()},
				}
			}
			t.Logf("/make_leave %s: prev_state_events=%v", userID, proto.PrevStateEvents)
			if onMakeLeave != nil {
				onMakeLeave(proto)
			}
			return util.JSONResponse{
				Code: 200,
				JSON: fclient.RespMakeLeave{
					RoomVersion: room.Version,
					LeaveEvent:  *proto,
				},
			}
		}),
	).Methods("GET")

	srv.Mux().HandleFunc("/_matrix/federation/v2/send_leave/{roomID}/{eventID}",
		srv.ValidFederationRequest(t, func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
			if pathParams["roomID"] != room.RoomID {
				return wrongRoom(pathParams["roomID"])
			}
			ev, err := gomatrixserverlib.MustGetRoomVersion(room.Version).NewEventFromUntrustedJSON(fr.Content())
			if err != nil {
				ct.Errorf(t, "/send_leave: failed to load the leave event: %s", err)
				return util.JSONResponse{
					Code: 400,
					JSON: map[string]string{"error": "complement: " + err.Error()},
				}
			}
			t.Logf("/send_leave %s: %s", ev.EventID(), string(ev.JSON()))
			room.AddEvent(ev)
			if onSendLeave != nil {
				onSendLeave(ev)
			}
			return util.JSONResponse{Code: 200, JSON: struct{}{}}
		}),
	).Methods("PUT")
}

// handleTransactionsForUnknownRooms handles /send and passes every PDU to the callback.
// federation.HandleTransactionRequests drops PDUs for rooms Complement doesn't know about, which is
// exactly the case we care about when we have been invited to a room we are not in.
func handleTransactionsForUnknownRooms(t *testing.T, srv *federation.Server, cb func(ev gomatrixserverlib.PDU)) {
	srv.Mux().HandleFunc("/_matrix/federation/v1/send/{transactionID}",
		srv.ValidFederationRequest(t, func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
			var txn gomatrixserverlib.Transaction
			if err := json.Unmarshal(fr.Content(), &txn); err != nil {
				ct.Errorf(t, "/send: failed to unmarshal transaction: %s", err)
				return util.JSONResponse{
					Code: 400,
					JSON: map[string]string{"error": "complement: " + err.Error()},
				}
			}
			verImpl := gomatrixserverlib.MustGetRoomVersion(roomVersion)
			resp := fclient.RespSend{PDUs: make(map[string]fclient.PDUResult)}
			for _, pduJSON := range txn.PDUs {
				ev, err := verImpl.NewEventFromUntrustedJSON(pduJSON)
				if err != nil {
					// not necessarily fatal: this may be an event for another room entirely
					t.Logf("/send: failed to load PDU: %s : %s", err, string(pduJSON))
					continue
				}
				t.Logf("/send: %s (%s)", ev.EventID(), ev.Type())
				resp.PDUs[ev.EventID()] = fclient.PDUResult{}
				if cb != nil {
					cb(ev)
				}
			}
			return util.JSONResponse{Code: 200, JSON: resp}
		}),
	).Methods("PUT")
}

// serveGetMissingEvents handles /get_missing_events by walking the given room's DAG. A server
// cannot fill in the state DAG for an out-of-band invite, but it is allowed to try, so serve the
// requests rather than fail them.
func serveGetMissingEvents(t *testing.T, srv *federation.Server, room *federation.ServerRoom) {
	srv.Mux().HandleFunc("/_matrix/federation/v1/get_missing_events/{roomID}", func(w http.ResponseWriter, req *http.Request) {
		body, err := extractGetMissingEventsRequest(room.RoomID, req)
		if err != nil {
			ct.Errorf(t, "bad /get_missing_events request: %s", err)
			w.WriteHeader(400)
			w.Write([]byte(err.Error()))
			return
		}
		sg := NewStateGraph()
		sg.WalkPrevEvents = !body.StateDAG
		room.TimelineMutex.RLock()
		sg.Update(room.Timeline)
		room.TimelineMutex.RUnlock()
		var resp fclient.RespMissingEvents
		for _, ev := range sg.GetMissingEvents(body.LatestEvents, body.Limit) {
			if ev == nil {
				continue // we don't know this event
			}
			resp.Events = append(resp.Events, ev.JSON())
		}
		t.Logf(
			"/get_missing_events state_dag=%v latest_events=%v limit=%d: returning %d events",
			body.StateDAG, body.LatestEvents, body.Limit, len(resp.Events),
		)
		w.WriteHeader(200)
		if err := json.NewEncoder(w).Encode(&resp); err != nil {
			ct.Errorf(t, "failed to encode the /get_missing_events response: %s", err)
		}
	})
}

// INV02-Inbound: a rescission which does not name the invite in its prev_state_events is ignored.
// The invited server cannot calculate the auth events for an out-of-band leave event, so that
// reference is the only thing tying the rescission to the invite it is rescinding. Without it,
// anyone could replay an old rescission to cancel a newer invite.
func TestMSC4242OutOfBandInviteRescindMustNameInvite(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	hs1 := deployment.GetFullyQualifiedHomeserverName(t, "hs1")

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	// Dave's rescission is a valid one and acts as a sentinel: the homeserver processes the events
	// in a room in order, so once Dave has left we know Alice's rescission has been dealt with too.
	dave := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleTransactionRequests(nil, nil),
		federation.HandleEventRequests(),
		federation.HandleMakeSendJoinRequests(),
	)
	srv.UnexpectedRequestsAreErrors = false
	cancel := srv.Listen()
	defer cancel()
	bob := srv.UserID("bob")

	room := srv.MustMakeRoom(t, roomVersion,
		federation.InitialRoomEvents(roomVersion, bob),
		federation.WithImpl(ServerRoomImplStateDAG(t, srv)),
	)
	serveGetMissingEvents(t, srv, room)

	mustSendOutOfBandInvite(t, srv, deployment, hs1, room, bob, alice.UserID)
	daveInvite := mustSendOutOfBandInvite(t, srv, deployment, hs1, room, bob, dave.UserID)
	aliceSince := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, room.RoomID))
	daveSince := dave.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(dave.UserID, room.RoomID))

	// A state event to be the forward extremity of the state DAG, so that a rescission has
	// something other than the invites to point at.
	roomName := srv.MustCreateEvent(t, room, federation.Event{
		Type:     spec.MRoomName,
		StateKey: &empty,
		Sender:   bob,
		Content: map[string]interface{}{
			"name": "rescinding",
		},
	})
	room.AddEvent(roomName)

	// This rescission does not name Alice's invite, so it must be ignored.
	aliceRescind := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:     spec.MRoomMember,
			StateKey: &alice.UserID,
			Sender:   bob,
			Content: map[string]interface{}{
				"membership": spec.Leave,
				"reason":     "rescinding the invite",
			},
		},
		PrevStateEvents: []string{roomName.EventID()},
	})
	// This one does name Dave's invite, so it must be accepted.
	daveRescind := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:     spec.MRoomMember,
			StateKey: &dave.UserID,
			Sender:   bob,
			Content: map[string]interface{}{
				"membership": spec.Leave,
				"reason":     "rescinding the invite",
			},
		},
		PrevStateEvents: []string{roomName.EventID(), daveInvite.EventID()},
	})
	srv.MustSendTransaction(t, deployment, hs1, AsEventJSONs([]gomatrixserverlib.PDU{
		aliceRescind, daveRescind,
	}), nil)

	dave.MustSyncUntil(t, client.SyncReq{Since: daveSince}, client.SyncLeftFrom(dave.UserID, room.RoomID))

	// Alice's rescission was sent before Dave's, so the homeserver has already processed it.
	// It must not have taken effect.
	syncResp, _ := alice.MustSync(t, client.SyncReq{Since: aliceSince})
	must.Equal(
		t, syncResp.Get("rooms.leave."+client.GjsonEscape(room.RoomID)).Exists(), false,
		"Alice left the room due to a rescission which did not name her invite",
	)
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(alice.UserID, room.RoomID))
}
