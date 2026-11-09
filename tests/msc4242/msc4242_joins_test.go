package tests

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
)

// Test that a homeserver can join a public state DAG room over federation, leave it, then rejoin it.
//
// The rejoin is the interesting part: the homeserver already has most of the state DAG persisted
// from the first join, so the /send_join response is a mixture of events it has seen before and
// events which were added to the room while it was away.
func TestMSC4242JoinLeaveRejoinPublicRoom(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		// accept incoming presence transactions, membership events, etc
		federation.HandleTransactionRequests(nil, nil),
		// accept incoming /event requests
		federation.HandleEventRequests(),
		federation.HandleMakeSendJoinRequests(),
	)
	// the homeserver makes /query/profile and /user/devices requests we don't care about
	srv.UnexpectedRequestsAreErrors = false
	cancel := srv.Listen()
	defer cancel()

	bob := srv.UserID("bob")
	// InitialRoomEvents sets a join rule of "public", so no invite is needed to (re)join.
	room := srv.MustMakeRoom(t, roomVersion,
		federation.InitialRoomEvents(roomVersion, bob),
		federation.WithImpl(ServerRoomImplStateDAG(t)),
	)

	// Lengthen the state DAG before the join so the homeserver has to walk it rather than just
	// consume the room creation events.
	setDisplayName(t, srv, room, bob, "bob before the join", 3)
	mustSetRoomName(t, srv, room, bob, "before the join")

	// Alice should be able to join this room.
	alice.MustJoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})
	sinceJoined := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, room.RoomID))
	mustHaveStateEventContent(
		t, currentRoomState(t, alice, room.RoomID), spec.MRoomName, "", "name", "before the join",
		"current state at first join missing room name",
	)

	// Alice leaves, wait for it to propagate.
	alice.MustLeaveRoom(t, room.RoomID)
	alice.MustSyncUntil(t, client.SyncReq{Since: sinceJoined}, client.SyncLeftFrom(alice.UserID, room.RoomID))
	leaveEvent := awaitMembership(t, room, alice.UserID, "leave")
	t.Logf("state DAG: %s = (m.room.member, %s) leave prev_state_events=%v prev_events=%v",
		leaveEvent.EventID(), alice.UserID, leaveEvent.PrevStateEventIDs(), leaveEvent.PrevEventIDs())

	// Add more state while the homeserver is not in the room
	setDisplayName(t, srv, room, bob, "bob after the leave", 3)
	mustSetRoomName(t, srv, room, bob, "after the leave")

	// Alice rejoins the room
	alice.MustJoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, room.RoomID))
	rejoinEvent := awaitMembership(t, room, alice.UserID, "join")
	t.Logf("state DAG: %s = (m.room.member, %s) rejoin prev_state_events=%v prev_events=%v",
		rejoinEvent.EventID(), alice.UserID, rejoinEvent.PrevStateEventIDs(), rejoinEvent.PrevEventIDs())

	state := currentRoomState(t, alice, room.RoomID)
	mustHaveStateEventContent(
		t, state, spec.MRoomName, "", "name", "after the leave",
		"current state at rejoin has invalid room name event",
	)
	mustHaveStateEventContent(
		t, state, spec.MRoomMember, bob, "displayname", "bob after the leave 2",
		"current state at rejoin has a stale membership event for the remote user",
	)
	mustHaveStateEventContent(
		t, state, spec.MRoomMember, alice.UserID, "membership", "join",
		"the rejoining user is not joined",
	)

	// The room works after the rejoin: an event sent by the remote server arrives.
	msg := srv.MustCreateEvent(t, room, federation.Event{
		Type:   "m.room.message",
		Sender: bob,
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "I am sent after the rejoin",
		},
	})
	room.AddEvent(msg)
	srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{msg.JSON()}, nil)
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(room.RoomID, msg.EventID()))
}

// Test that a rejected event in the /send_join response, and the otherwise valid events which
// reference it via prev_state_events, are all rejected and so aren't part of the room's current state.
//
// MSC4242 specifies cascading rejection: "if A is rejected and B references A, then B is rejected
// and so on". A server which instead treats the state_dag as a flat set of state events (e.g.
// applying them in depth order) will pick up the rejected events and fail this test.
//
// The state DAG we build is:
//
//	           ALICE_LEAVE
//	           /         \
//	     TOPIC            NAME            <- both valid, both sent by bob
//	       |                 \
//	 DORIS_NAME (rejected: doris is not in the room)
//	       |                   \
//	CHARLIE_JOIN (valid on its own, rejected for referencing DORIS_NAME)
//	       |                     \
//	 BOB_ROOM_AVATAR (valid on its own, rejected two hops from DORIS_NAME)
//	                               \
//	                      ALICE_REJOIN, prev_state_events = [NAME]
//
// The rejected events are on a branch alongside the join: an event
// referencing a rejected event is itself rejected, so if the join could reach them the join would
// be rejected too.
func TestMSC4242JoinPublicRoomWithRejectedStateDAGEvents(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

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
	charlie := srv.UserID("charlie")
	doris := srv.UserID("doris")
	room := srv.MustMakeRoom(t, roomVersion,
		federation.InitialRoomEvents(roomVersion, bob),
		federation.WithImpl(ServerRoomImplStateDAG(t)),
	)

	// Join then leave, so that the fork point is a state event the homeserver already knows about.
	alice.MustJoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})
	sinceJoined := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, room.RoomID))
	alice.MustLeaveRoom(t, room.RoomID)
	alice.MustSyncUntil(t, client.SyncReq{Since: sinceJoined}, client.SyncLeftFrom(alice.UserID, room.RoomID))
	leaveEvent := awaitMembership(t, room, alice.UserID, "leave")

	// Fork the state DAG at the leave event.
	topic := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       spec.MRoomTopic,
			Sender:     bob,
			StateKey:   &empty,
			Content:    map[string]interface{}{"topic": "fork containing rejected events"},
			PrevEvents: []string{leaveEvent.EventID()},
		},
		PrevStateEvents: []string{leaveEvent.EventID()},
	})
	room.AddEvent(topic)
	name := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       spec.MRoomName,
			Sender:     bob,
			StateKey:   &empty,
			Content:    map[string]interface{}{"name": "fork containing rejoin"},
			PrevEvents: []string{leaveEvent.EventID()},
		},
		PrevStateEvents: []string{leaveEvent.EventID()},
	})
	room.AddEvent(name)

	// Hang the rejected events off the topic event. Doris is not in the room, so her event fails
	// auth and is rejected.
	dorisName := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       spec.MRoomName,
			Sender:     doris,
			StateKey:   &empty,
			Content:    map[string]interface{}{"name": "doris is not in the room so this is rejected"},
			PrevEvents: []string{topic.EventID()},
		},
		PrevStateEvents: []string{topic.EventID()},
	})
	room.AddEvent(dorisName)
	// Charlie's join would be allowed on its own as the room is public.
	// It is rejected because it references a rejected event in prev_state_events.
	charlieJoin := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       spec.MRoomMember,
			Sender:     charlie,
			StateKey:   &charlie,
			Content:    map[string]interface{}{"membership": spec.Join},
			PrevEvents: []string{dorisName.EventID()},
		},
		PrevStateEvents: []string{dorisName.EventID()},
	})
	room.AddEvent(charlieJoin)
	// Bob may set the room avatar, and this event references a valid event, but it is rejected because
	// that event is itself rejected two hops back.
	bobAvatar := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       spec.MRoomAvatar,
			Sender:     bob,
			StateKey:   &empty,
			Content:    map[string]interface{}{"avatar": "rejected: two hops from a rejected event"},
			PrevEvents: []string{charlieJoin.EventID()},
		},
		PrevStateEvents: []string{charlieJoin.EventID()},
	})
	room.AddEvent(bobAvatar)

	t.Logf(
		"leave=%s topic=%s name=%s dorisName=%s charlieJoin=%s bobAvatar=%s",
		leaveEvent.EventID(), topic.EventID(), name.EventID(),
		dorisName.EventID(), charlieJoin.EventID(), bobAvatar.EventID(),
	)

	// Point the rejoin at the name event.
	// ProtoEventCreatorFn will read this when it services the /make_join request and set this as prev_state_events
	// but we return ALL state events in the room.Timeline when returning the state DAG in the /send_join response,
	// meaning we will return the rejected fork.
	room.ForwardExtremities = []string{name.EventID()}

	alice.MustJoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, room.RoomID))

	state := currentRoomState(t, alice, room.RoomID)
	// Both forks should be in the current state
	mustHaveStateEventContent(
		t, state, spec.MRoomTopic, "", "topic", "fork containing rejected events",
		"the rejected fork was not merged into the current state",
	)
	mustHaveStateEventContent(
		t, state, spec.MRoomName, "", "name", "fork containing rejoin",
		"current state not calculated correctly",
	)
	mustNotHaveStateEvent(
		t, state, spec.MRoomMember, charlie,
		"charlie's join references a rejected event so must itself be rejected",
	)
	mustNotHaveStateEvent(
		t, state, spec.MRoomAvatar, "",
		"bob's room avatar references a rejected event 2 hops back so must itself be rejected",
	)
}

// Test that rejection cascades across a rejoin: an event which references an event the server
// already knows to be rejected must itself be rejected, even though the rejected event is not
// reprocessed because the server has seen it before.
//
// This is the same cascading rejection rule as
// TestMSC4242JoinPublicRoomWithRejectedStateDAGEvents, but split over two joins. The rejected
// event arrives in the first /send_join response and is persisted as rejected. The events built
// on top of it only arrive in the second /send_join response, by which time the rejected event
// is an event the server has already seen, so it is filtered out of the batch being processed.
// A server which only tracks the rejection status of events in the batch it is currently
// processing will accept the new events and fail this test.
//
// The state DAG we build is:
//
//	            BASE (m.room.join_rules, last of the initial room events)
//	           /    \
//	TOPIC_BEFORE     DORIS_NAME (rejected: doris is not in the room)
//	     |                |
//	ALICE_JOIN            |   <- first join, prev_state_events = [TOPIC_BEFORE]
//	     |                |
//	ALICE_LEAVE           |
//	     |                |
//	TOPIC_AFTER           |   <- rejoin, prev_state_events = [TOPIC_AFTER]
//	                      |
//	                 CHARLIE_JOIN (valid on its own, rejected for referencing DORIS_NAME)
//	                      |
//	                   BOB_NAME (valid on its own, rejected two hops from DORIS_NAME)
//
// Every event which sets the room name is on the rejected branch, so a conformant server never
// has an (m.room.name, "") event in the room state.
func TestMSC4242RejectionCascadesOnRejoin(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

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
	charlie := srv.UserID("charlie")
	doris := srv.UserID("doris")
	room := srv.MustMakeRoom(t, roomVersion,
		federation.InitialRoomEvents(roomVersion, bob),
		federation.WithImpl(ServerRoomImplStateDAG(t)),
	)
	base := room.ForwardExtremities[0]

	// Fork the state DAG at the last of the initial room events: one branch is where the
	// homeserver joins, the other is where the rejected events live.
	topicBefore := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       spec.MRoomTopic,
			Sender:     bob,
			StateKey:   &empty,
			Content:    map[string]interface{}{"topic": "before the join"},
			PrevEvents: []string{base},
		},
		PrevStateEvents: []string{base},
	})
	room.AddEvent(topicBefore)
	// Doris is not in the room, so her event fails auth and is rejected.
	dorisName := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       spec.MRoomName,
			Sender:     doris,
			StateKey:   &empty,
			Content:    map[string]interface{}{"name": "doris is not in the room so this is rejected"},
			PrevEvents: []string{base},
		},
		PrevStateEvents: []string{base},
	})
	room.AddEvent(dorisName)

	// Join on the clean branch: the rejected event is in the /send_join response, but the join
	// does not reference it, so only the rejected event itself is rejected.
	room.ForwardExtremities = []string{topicBefore.EventID()}
	alice.MustJoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})
	sinceJoined := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, room.RoomID))

	// The rejected event must not be part of the room state after the first join. If it is, the
	// rest of this test is meaningless as there is no rejection to cascade.
	mustNotHaveStateEvent(
		t, currentRoomState(t, alice, room.RoomID), spec.MRoomName, "",
		"doris is not in the room so her name event must be rejected",
	)

	// Alice leaves, wait for it to propagate.
	alice.MustLeaveRoom(t, room.RoomID)
	alice.MustSyncUntil(t, client.SyncReq{Since: sinceJoined}, client.SyncLeftFrom(alice.UserID, room.RoomID))
	leaveEvent := awaitMembership(t, room, alice.UserID, "leave")

	// Extend the clean branch past the leave: this is where the rejoin will hang off.
	topicAfter := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       spec.MRoomTopic,
			Sender:     bob,
			StateKey:   &empty,
			Content:    map[string]interface{}{"topic": "after the leave"},
			PrevEvents: []string{leaveEvent.EventID()},
		},
		PrevStateEvents: []string{leaveEvent.EventID()},
	})
	room.AddEvent(topicAfter)

	// Extend the rejected branch while the homeserver is not in the room. These events are new to
	// the homeserver, but the event they descend from is not: it was rejected during the first join.
	//
	// Charlie's join would be allowed on its own as the room is public. It is rejected because it
	// references a rejected event in prev_state_events.
	charlieJoin := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       spec.MRoomMember,
			Sender:     charlie,
			StateKey:   &charlie,
			Content:    map[string]interface{}{"membership": spec.Join},
			PrevEvents: []string{dorisName.EventID()},
		},
		PrevStateEvents: []string{dorisName.EventID()},
	})
	room.AddEvent(charlieJoin)
	// Bob may set the room name, and this event references a valid event, but it is rejected because
	// that event is itself rejected two hops back.
	bobName := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       spec.MRoomName,
			Sender:     bob,
			StateKey:   &empty,
			Content:    map[string]interface{}{"name": "rejected: two hops from a rejected event"},
			PrevEvents: []string{charlieJoin.EventID()},
		},
		PrevStateEvents: []string{charlieJoin.EventID()},
	})
	room.AddEvent(bobName)

	t.Logf(
		"base=%s topicBefore=%s dorisName=%s leave=%s topicAfter=%s charlieJoin=%s bobName=%s",
		base, topicBefore.EventID(), dorisName.EventID(), leaveEvent.EventID(),
		topicAfter.EventID(), charlieJoin.EventID(), bobName.EventID(),
	)

	// Rejoin on the clean branch.
	room.ForwardExtremities = []string{topicAfter.EventID()}
	alice.MustJoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, room.RoomID))

	state := currentRoomState(t, alice, room.RoomID)
	mustHaveStateEventContent(
		t, state, spec.MRoomMember, alice.UserID, "membership", "join",
		"the rejoining user is not joined",
	)
	mustHaveStateEventContent(
		t, state, spec.MRoomTopic, "", "topic", "after the leave",
		"current state at rejoin was not calculated from the state DAG",
	)
	mustNotHaveStateEvent(
		t, state, spec.MRoomMember, charlie,
		"charlie's join references an event which was rejected before the rejoin so must itself be rejected",
	)
	mustNotHaveStateEvent(
		t, state, spec.MRoomName, "",
		"every event setting the room name descends from a rejected event so must itself be rejected",
	)
}

// Test that a rejoin works when an event in the /send_join response merges a state DAG branch the
// server already has with a branch which is new in that response.
//
// The homeserver joins on the main branch, leaves, then rejoins. A side branch created before the
// first join is never extended, so it stays a state DAG head throughout and is persisted during
// the first join. The merge event which joins it back to the main branch only arrives in the
// second /send_join response, so its prev_state_events mix an event the server persisted a join
// ago with an event which is still only in the batch being processed. A server which can only use
// the state it remembered for events in the current batch, or only the state in its database,
// cannot resolve this event.
//
// The state DAG we build is:
//
//	            BASE (m.room.join_rules, last of the initial room events)
//	           /    \
//	  MAIN_TOPIC     SIDE_NAME        <- both valid, both persisted during the first join
//	      |             |
//	 ALICE_JOIN         |             <- first join, prev_state_events = [MAIN_TOPIC]
//	      |             |
//	 BOB_KICK_ALICE     |
//	      |             |
//	POST_LEAVE_TOPIC    |             <- new to the homeserver at the rejoin
//	           \        |
//	            \       |
//	             MERGE                <- prev_state_events = [POST_LEAVE_TOPIC, SIDE_NAME]
//	               |
//	          ALICE_REJOIN            <- rejoin, prev_state_events = [MERGE]
//
// The state at MERGE is the resolution of both branches, so the room name set on the side branch
// and the topic set on the main branch must both be in the current state after the rejoin.
func TestMSC4242RejoinMergesOldAndNewStateDAGBranches(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

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
		federation.WithImpl(ServerRoomImplStateDAG(t)),
	)
	base := room.ForwardExtremities[0]

	// Fork the state DAG at the last of the initial room events. The homeserver joins on the main
	// branch; nothing ever extends the side branch, so it remains a head of the state DAG.
	mainTopic := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       spec.MRoomTopic,
			Sender:     bob,
			StateKey:   &empty,
			Content:    map[string]interface{}{"topic": "before the join"},
			PrevEvents: []string{base},
		},
		PrevStateEvents: []string{base},
	})
	room.AddEvent(mainTopic)
	sideName := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       spec.MRoomName,
			Sender:     bob,
			StateKey:   &empty,
			Content:    map[string]interface{}{"name": "set on the side branch"},
			PrevEvents: []string{base},
		},
		PrevStateEvents: []string{base},
	})
	room.AddEvent(sideName)

	// Join on the main branch. The side branch is in the /send_join response, so it is persisted
	// during this join: this is what makes it an event the homeserver has already seen when it
	// rejoins, and so an event which is not reprocessed as part of that batch.
	room.ForwardExtremities = []string{mainTopic.EventID()}
	alice.MustJoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})
	sinceJoined := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, room.RoomID))
	stateAtFirstJoin := currentRoomState(t, alice, room.RoomID)
	mustHaveStateEventContent(
		t, stateAtFirstJoin, spec.MRoomName, "", "name", "set on the side branch",
		"the side branch was not persisted during the first join, so the rejoin cannot exercise a merge",
	)
	// Both branches must already be merged into the current state here. If they are not, the
	// server is losing a fork before the rejoin is even involved, and the rest of this test says
	// nothing about how it handles a merge of an old branch with a new one.
	mustHaveStateEventContent(
		t, stateAtFirstJoin, spec.MRoomTopic, "", "topic", "before the join",
		"the branch the homeserver joined on is not in the current state after the first join",
	)
	aliceJoinEventID := stateAtFirstJoin[[2]string{spec.MRoomMember, alice.UserID}].Get("event_id").Str
	if aliceJoinEventID == "" {
		ct.Fatalf(t, "failed to find alice's join event")
	}

	// Bob kicks Alice, ensuring it never references SIDE_NAME in the kick, wait for it to propagate.
	bobKickAlice := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       spec.MRoomMember,
			Sender:     bob,
			StateKey:   &alice.UserID,
			Content:    map[string]interface{}{"membership": spec.Leave},
			PrevEvents: []string{aliceJoinEventID},
		},
		PrevStateEvents: []string{aliceJoinEventID},
	})
	room.AddEvent(bobKickAlice)
	srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{bobKickAlice.JSON()}, nil)
	alice.MustSyncUntil(t, client.SyncReq{Since: sinceJoined}, client.SyncLeftFrom(alice.UserID, room.RoomID))
	leaveEvent := awaitMembership(t, room, alice.UserID, "leave")

	// Extend the main branch while the homeserver is not in the room: this event is new to it at
	// the rejoin.
	postLeaveTopic := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:       spec.MRoomTopic,
			Sender:     bob,
			StateKey:   &empty,
			Content:    map[string]interface{}{"topic": "after the leave"},
			PrevEvents: []string{leaveEvent.EventID()},
		},
		PrevStateEvents: []string{leaveEvent.EventID()},
	})
	room.AddEvent(postLeaveTopic)

	// Merge the two branches. One parent is new in the upcoming /send_join response, the other was
	// persisted during the first join.
	merge := mustCreateEvent(t, srv, room, MSC4242Event{
		Event: federation.Event{
			Type:     spec.MRoomMember,
			Sender:   bob,
			StateKey: &bob,
			Content: map[string]interface{}{
				"membership":  spec.Join,
				"displayname": "bob merged the branches",
			},
			PrevEvents: []string{postLeaveTopic.EventID(), sideName.EventID()},
		},
		PrevStateEvents: []string{postLeaveTopic.EventID(), sideName.EventID()},
	})
	room.AddEvent(merge)

	t.Logf(
		"base=%s mainTopic=%s sideName=%s leave=%s postLeaveTopic=%s merge=%s",
		base, mainTopic.EventID(), sideName.EventID(), leaveEvent.EventID(),
		postLeaveTopic.EventID(), merge.EventID(),
	)
	// The merge must reference one event which is new in the upcoming /send_join response
	// (postLeaveTopic) and one which the homeserver persisted during the first join (sideName).
	t.Logf("merge %s prev_state_events=%v prev_events=%v",
		merge.EventID(), merge.PrevStateEventIDs(), merge.PrevEventIDs())
	t.Logf("leave %s prev_state_events=%v", leaveEvent.EventID(), leaveEvent.PrevStateEventIDs())

	// Rejoin at the merge event.
	room.ForwardExtremities = []string{merge.EventID()}
	alice.MustJoinRoom(t, room.RoomID, []spec.ServerName{srv.ServerName()})
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, room.RoomID))

	state := currentRoomState(t, alice, room.RoomID)
	mustHaveStateEventContent(
		t, state, spec.MRoomMember, alice.UserID, "membership", "join",
		"the rejoining user is not joined",
	)
	mustHaveStateEventContent(
		t, state, spec.MRoomMember, bob, "displayname", "bob merged the branches",
		"the merge event is not part of the current state",
	)
	mustHaveStateEventContent(
		t, state, spec.MRoomTopic, "", "topic", "after the leave",
		"the branch which was new in the rejoin's /send_join response was dropped from the merged state",
	)
	mustHaveStateEventContent(
		t, state, spec.MRoomName, "", "name", "set on the side branch",
		"the branch persisted before the rejoin was dropped from the merged state",
	)
}

// setDisplayName sends numTimes membership events for userID which each change the display name,
// lengthening the state DAG. The events are added to the room but not sent anywhere: they are
// picked up by servers when they next join.
func setDisplayName(t *testing.T, srv *federation.Server, room *federation.ServerRoom, userID, prefix string, numTimes int) {
	t.Helper()
	for i := 0; i < numTimes; i++ {
		time.Sleep(time.Millisecond) // ensure origin_server_ts changes
		displayName := fmt.Sprintf("%s %d", prefix, i)
		ev := mustAddStateEvent(t, srv, room, federation.Event{
			Type:     spec.MRoomMember,
			Sender:   userID,
			StateKey: &userID,
			Content: map[string]interface{}{
				"membership":  spec.Join,
				"displayname": displayName,
			},
		})
		t.Logf("state DAG: %s = (m.room.member, %s) displayname=%q prev_state_events=%v",
			ev.EventID(), userID, displayName, ev.PrevStateEventIDs())
	}
}

// mustSetRoomName adds an m.room.name to the Complement room and logs where it sits in the DAG.
func mustSetRoomName(t *testing.T, srv *federation.Server, room *federation.ServerRoom, sender, name string) gomatrixserverlib.PDU {
	t.Helper()
	ev := mustAddStateEvent(t, srv, room, federation.Event{
		Type:     spec.MRoomName,
		Sender:   sender,
		StateKey: &empty,
		Content:  map[string]interface{}{"name": name},
	})
	t.Logf("state DAG: %s = (m.room.name, \"\") name=%q prev_state_events=%v",
		ev.EventID(), name, ev.PrevStateEventIDs())
	return ev
}
