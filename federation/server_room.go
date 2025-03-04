package federation

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/matrix-org/gomatrixserverlib/spec"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/helpers"
)

type Event struct {
	Type     string
	Sender   string
	StateKey *string
	Content  map[string]interface{}

	Unsigned map[string]interface{}
	// The events needed to authenticate this event.
	// This can be either []EventReference for room v1/v2, or []string for room v3 onwards.
	// If it is left at nil, MustCreateEvent will populate it automatically based on the room state.
	AuthEvents interface{}
	// The prev events of the event if we want to override or falsify them.
	// If it is left at nil, MustCreateEvent will populate it automatically based on the forward extremities.
	PrevEvents interface{}
	// If this is a redaction, the event that it redacts
	Redacts string
}

// ServerRoomOpt are options that can configure ServerRooms
type ServerRoomOpt func(r *ServerRoom)

// WithRoomID configures the room to have the given room ID
func WithRoomID(roomID string) ServerRoomOpt {
	return func(r *ServerRoom) {
		r.RoomID = roomID
	}
}

// WithImpl configures the room to have the given ServerRoomImpl.
// Useful for custom rooms.
func WithImpl(impl ServerRoomImpl) ServerRoomOpt {
	return func(r *ServerRoom) {
		r.ServerRoomImpl = impl
	}
}

// EXPERIMENTAL
// ServerRoom represents a room on this test federation server
type ServerRoom struct {
	// Functions to map Complement events into actual PDUs.
	// Most tests don't care about this and can use the default functions,
	// but if your MSC or tests fiddle with the raw JSON in some way then these
	// function need to be replaced. By replacing these functions, helper functions
	// which indirectly create events (e.g joins and leaves) will automatically use
	// them and will hence work with your custom code.
	ServerRoomImpl

	Version            gomatrixserverlib.RoomVersion
	RoomID             string
	State              map[string]gomatrixserverlib.PDU
	StateMutex         sync.RWMutex
	Timeline           []gomatrixserverlib.PDU
	TimelineMutex      sync.RWMutex
	ForwardExtremities []string
	Depth              int64
	waiters            map[string][]*helpers.Waiter // room ID -> []Waiter
	waitersMu          *sync.Mutex
}

// NewServerRoom creates an empty room structure with no events
func NewServerRoom(roomVer gomatrixserverlib.RoomVersion, roomId string) *ServerRoom {
	room := &ServerRoom{
		RoomID:             roomId,
		Version:            roomVer,
		State:              make(map[string]gomatrixserverlib.PDU),
		ForwardExtremities: make([]string, 0),
		waiters:            make(map[string][]*helpers.Waiter),
		waitersMu:          &sync.Mutex{},
	}
	room.ServerRoomImpl = &ServerRoomImplDefault{}
	return room
}

// AddEvent adds a new event to the timeline, updating current state if it is a state event.
// Updates depth and forward extremities.
func (r *ServerRoom) AddEvent(ev gomatrixserverlib.PDU) {
	if ev.StateKey() != nil {
		r.ReplaceCurrentState(ev)
	}
	r.TimelineMutex.Lock()
	r.Timeline = append(r.Timeline, ev)
	r.TimelineMutex.Unlock()
	// update extremities and depth
	if ev.Depth() > r.Depth {
		r.Depth = ev.Depth()
	}
	r.ForwardExtremities = []string{ev.EventID()}

	// inform waiters
	r.waitersMu.Lock()
	defer r.waitersMu.Unlock()
	for _, w := range r.waiters[ev.EventID()] {
		w.Finish()
	}
	delete(r.waiters, ev.EventID()) // clear the waiters
}

// WaiterForEvent creates a Waiter which waits until the given event ID is added to the room.
// This can be used as a synchronisation point to wait until the server under test has sent
// a given PDU in a /send transaction to the Complement server. This is the equivalent to listening
// for the PDU in the /send transaction and then unblocking the Waiter. Note that calling
// this function doesn't actually block. Call .Wait(time.Duration) on the waiter to block.
//
// Note: you must still add HandleTransactionRequests(nil,nil) to your server for the server to
// automatically add events to the room.
func (r *ServerRoom) WaiterForEvent(eventID string) *helpers.Waiter {
	// we need to lock the timeline so we can check the timeline without racing. We need to check
	// the timeline so we can immediately finish the waiter if the event ID is already in the timeline.
	r.TimelineMutex.Lock()
	defer r.TimelineMutex.Unlock()
	w := helpers.NewWaiter()
	r.waitersMu.Lock()
	r.waiters[eventID] = append(r.waiters[eventID], w)
	r.waitersMu.Unlock()
	// check if the event is already there and if so immediately end the wait
	for _, ev := range r.Timeline {
		if ev.EventID() == eventID {
			w.Finish()
			break
		}
	}
	return w
}

// AuthEvents returns the state event IDs of the auth events which authenticate this event
func (r *ServerRoom) AuthEvents(sn gomatrixserverlib.StateNeeded) (eventIDs []string) {
	// Guard against returning a nil string slice
	eventIDs = make([]string, 0)

	appendIfExists := func(evType, stateKey string) {
		ev := r.CurrentState(evType, stateKey)
		if ev == nil {
			return
		}
		eventIDs = append(eventIDs, ev.EventID())
	}
	if sn.Create {
		appendIfExists("m.room.create", "")
	}
	if sn.JoinRules {
		appendIfExists("m.room.join_rules", "")
	}
	if sn.PowerLevels {
		appendIfExists("m.room.power_levels", "")
	}
	for _, mem := range sn.Member {
		appendIfExists("m.room.member", mem)
	}
	return
}

// ReplaceCurrentState inserts a new state event for this room or replaces current state depending
// on the (type, state_key) provided. The event provided must be a state event.
func (r *ServerRoom) ReplaceCurrentState(ev gomatrixserverlib.PDU) {
	tuple := fmt.Sprintf("%s\x1f%s", ev.Type(), *ev.StateKey())
	r.StateMutex.Lock()
	r.State[tuple] = ev
	r.StateMutex.Unlock()
}

// CurrentState returns the state event for the given (type, state_key) or nil.
func (r *ServerRoom) CurrentState(evType, stateKey string) gomatrixserverlib.PDU {
	tuple := fmt.Sprintf("%s\x1f%s", evType, stateKey)
	r.StateMutex.RLock()
	state := r.State[tuple]
	r.StateMutex.RUnlock()
	return state
}

// AllCurrentState returns all the current state events
func (r *ServerRoom) AllCurrentState() (events []gomatrixserverlib.PDU) {
	r.StateMutex.RLock()
	for _, ev := range r.State {
		events = append(events, ev)
	}
	r.StateMutex.RUnlock()
	return
}

// AuthChain returns all auth events for all events in the current state TODO: recursively
func (r *ServerRoom) AuthChain() (chain []gomatrixserverlib.PDU) {
	return r.AuthChainForEvents(r.AllCurrentState())
}

// AuthChainForEvents returns all auth events for all events in the given state
func (r *ServerRoom) AuthChainForEvents(events []gomatrixserverlib.PDU) (chain []gomatrixserverlib.PDU) {
	chainMap := make(map[string]bool)

	// build a map of all events in the room
	// Timeline and State contain different sets of events, so check them both.
	eventsByID := map[string]gomatrixserverlib.PDU{}
	r.TimelineMutex.RLock()
	for _, ev := range r.Timeline {
		eventsByID[ev.EventID()] = ev
	}
	r.TimelineMutex.RUnlock()
	r.StateMutex.RLock()
	for _, ev := range r.State {
		eventsByID[ev.EventID()] = ev
	}
	r.StateMutex.RUnlock()

	// a queue of events whose auth events are to be included in the auth chain
	queue := []gomatrixserverlib.PDU{}
	queue = append(queue, events...)

	// get all the auth events recursively
	// we extend the "queue" as we go along
	for i := 0; i < len(queue); i++ {
		ev := queue[i]
		for _, evID := range ev.AuthEventIDs() {
			if chainMap[evID] {
				continue
			}
			chainMap[evID] = true
			event, ok := eventsByID[evID]
			if !ok {
				panic(fmt.Sprintf("AuthChainForEvents: event %s refers to unknown event %s in auth events", ev.EventID(), evID))
			}
			chain = append(chain, event)
			queue = append(queue, event)
		}
	}

	return
}

// Check that the user currently has the membership provided in this room. Fails the test if not.
func (r *ServerRoom) MustHaveMembershipForUser(t ct.TestLike, userID, wantMembership string) {
	t.Helper()
	state := r.CurrentState("m.room.member", userID)
	if state == nil {
		ct.Fatalf(t, "no membership state for %s", userID)
	}
	m, err := state.Membership()
	if err != nil {
		ct.Fatalf(t, "m.room.member event exists for %s but cannot read membership field: %s", userID, err)
	}
	if m != wantMembership {
		ct.Fatalf(t, "incorrect membership state for %s: got %s, want %s", userID, m, wantMembership)
	}
}

// ServersInRoom gets all servers currently joined to the room
func (r *ServerRoom) ServersInRoom() (servers []string) {
	serverSet := make(map[string]struct{})

	r.StateMutex.RLock()
	for _, ev := range r.State {
		if ev.Type() != "m.room.member" {
			continue
		}
		membership, err := ev.Membership()
		if err != nil || membership != "join" {
			continue
		}
		_, server, err := gomatrixserverlib.SplitID('@', *ev.StateKey())
		if err != nil {
			continue
		}

		serverSet[string(server)] = struct{}{}
	}
	r.StateMutex.RUnlock()

	for server := range serverSet {
		servers = append(servers, server)
	}

	return
}

// Fetches the event with given event ID from the room timeline.
func (r *ServerRoom) GetEventInTimeline(eventID string) (gomatrixserverlib.PDU, bool) {
	r.TimelineMutex.Lock()
	defer r.TimelineMutex.Unlock()

	for _, ev := range r.Timeline {
		if ev.EventID() == eventID {
			return ev, true
		}
	}

	return nil, false
}

func initialPowerLevelsContent(roomCreator string) (c gomatrixserverlib.PowerLevelContent) {
	c.Defaults()
	c.Events = map[string]int64{
		"m.room.name":               50,
		"m.room.power_levels":       100,
		"m.room.history_visibility": 100,
		"m.room.canonical_alias":    50,
		"m.room.avatar":             50,
		"m.room.aliases":            0, // anyone can publish aliases by default. Has to be 0 else state_default is used.
	}
	c.Users = map[string]int64{roomCreator: 100}
	return c
}

// InitialRoomEvents returns the initial set of events that get created when making a room.
func InitialRoomEvents(roomVer gomatrixserverlib.RoomVersion, creator string) []Event {
	// need to serialise/deserialise to get map[string]interface{} annoyingly
	plContent := initialPowerLevelsContent(creator)
	plBytes, _ := json.Marshal(plContent)
	var plContentMap map[string]interface{}
	json.Unmarshal(plBytes, &plContentMap)
	return []Event{
		{
			Type:     "m.room.create",
			StateKey: b.Ptr(""),
			Sender:   creator,
			Content: map[string]interface{}{
				"creator":      creator,
				"room_version": roomVer,
			},
		},
		{
			Type:     "m.room.member",
			StateKey: b.Ptr(creator),
			Sender:   creator,
			Content: map[string]interface{}{
				"membership": "join",
			},
		},
		{
			Type:     "m.room.power_levels",
			StateKey: b.Ptr(""),
			Sender:   creator,
			Content:  plContentMap,
		},
		{
			Type:     "m.room.join_rules",
			StateKey: b.Ptr(""),
			Sender:   creator,
			Content: map[string]interface{}{
				"join_rule": "public",
			},
		},
	}
}

// EventIDsOrReferences converts a list of events into a list of EventIDs or EventReferences,
// depending on the room version
func (r *ServerRoom) EventIDsOrReferences(events []gomatrixserverlib.PDU) (refs []interface{}) {
	refs = make([]interface{}, len(events))
	for i, ev := range events {
		refs[i] = ev.EventID()
	}
	return
}

type ServerRoomImpl interface {
	// ProtoEventCreator converts a Complement Event into a gomatrixserverlib proto event, ready to be signed.
	// This function is used in /make_x endpoints to create proto events to return to other servers.
	// This function is one of two used when creating events, the other being EventCreator.
	ProtoEventCreator(room *ServerRoom, ev Event) (*gomatrixserverlib.ProtoEvent, error)
	// EventCreator converts a proto event into a signed PDU.
	EventCreator(room *ServerRoom, s *Server, proto *gomatrixserverlib.ProtoEvent) (gomatrixserverlib.PDU, error)
	// PopulateFromSendJoinResponse should replace the state of this ServerRoom with the information contained
	// in RespSendJoin and the join event.
	PopulateFromSendJoinResponse(room *ServerRoom, joinEvent gomatrixserverlib.PDU, resp fclient.RespSendJoin)
	// GenerateSendJoinResponse generates a /send_join response to send back to a server.
	GenerateSendJoinResponse(room *ServerRoom, s *Server, joinEvent gomatrixserverlib.PDU, expectPartialState, omitServersInRoom bool) fclient.RespSendJoin
}

type ServerRoomImplCustom struct {
	ServerRoomImplDefault
	ProtoEventCreatorFn            func(def ServerRoomImpl, room *ServerRoom, ev Event) (*gomatrixserverlib.ProtoEvent, error)
	EventCreatorFn                 func(def ServerRoomImpl, room *ServerRoom, s *Server, proto *gomatrixserverlib.ProtoEvent) (gomatrixserverlib.PDU, error)
	PopulateFromSendJoinResponseFn func(def ServerRoomImpl, room *ServerRoom, joinEvent gomatrixserverlib.PDU, resp fclient.RespSendJoin)
	GenerateSendJoinResponseFn     func(def ServerRoomImpl, room *ServerRoom, s *Server, joinEvent gomatrixserverlib.PDU, expectPartialState, omitServersInRoom bool) fclient.RespSendJoin
}

func (i *ServerRoomImplCustom) ProtoEventCreator(room *ServerRoom, ev Event) (*gomatrixserverlib.ProtoEvent, error) {
	if i.ProtoEventCreatorFn != nil {
		return i.ProtoEventCreatorFn(&i.ServerRoomImplDefault, room, ev)
	}
	return i.ServerRoomImplDefault.ProtoEventCreator(room, ev)
}

func (i *ServerRoomImplCustom) EventCreator(room *ServerRoom, s *Server, proto *gomatrixserverlib.ProtoEvent) (gomatrixserverlib.PDU, error) {
	if i.EventCreatorFn != nil {
		return i.EventCreatorFn(&i.ServerRoomImplDefault, room, s, proto)
	}
	return i.ServerRoomImplDefault.EventCreator(room, s, proto)
}

func (i *ServerRoomImplCustom) PopulateFromSendJoinResponse(room *ServerRoom, joinEvent gomatrixserverlib.PDU, resp fclient.RespSendJoin) {
	if i.PopulateFromSendJoinResponseFn != nil {
		i.PopulateFromSendJoinResponseFn(&i.ServerRoomImplDefault, room, joinEvent, resp)
		return
	}
	i.ServerRoomImplDefault.PopulateFromSendJoinResponse(room, joinEvent, resp)
}

func (i *ServerRoomImplCustom) GenerateSendJoinResponse(room *ServerRoom, s *Server, joinEvent gomatrixserverlib.PDU, expectPartialState, omitServersInRoom bool) fclient.RespSendJoin {
	if i.GenerateSendJoinResponseFn != nil {
		return i.GenerateSendJoinResponseFn(&i.ServerRoomImplDefault, room, s, joinEvent, expectPartialState, omitServersInRoom)
	}
	return i.ServerRoomImplDefault.GenerateSendJoinResponse(room, s, joinEvent, expectPartialState, omitServersInRoom)
}

type ServerRoomImplDefault struct{}

func (i *ServerRoomImplDefault) ProtoEventCreator(room *ServerRoom, ev Event) (*gomatrixserverlib.ProtoEvent, error) {
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
	}
	if err := proto.SetContent(ev.Content); err != nil {
		return nil, fmt.Errorf("EventCreator: failed to marshal event content: %s - %+v", err, ev.Content)
	}
	if err := proto.SetUnsigned(ev.Content); err != nil {
		return nil, fmt.Errorf("EventCreator: failed to marshal event unsigned: %s - %+v", err, ev.Unsigned)
	}
	if proto.AuthEvents == nil {
		var stateNeeded gomatrixserverlib.StateNeeded
		stateNeeded, err := gomatrixserverlib.StateNeededForProtoEvent(&proto)
		if err != nil {
			return nil, fmt.Errorf("EventCreator: failed to work out auth_events : %s", err)
		}
		proto.AuthEvents = room.AuthEvents(stateNeeded)
	}
	return &proto, nil
}

func (i *ServerRoomImplDefault) EventCreator(room *ServerRoom, s *Server, proto *gomatrixserverlib.ProtoEvent) (gomatrixserverlib.PDU, error) {
	verImpl, err := gomatrixserverlib.GetRoomVersion(room.Version)
	if err != nil {
		return nil, fmt.Errorf("EventCreator: invalid room version: %s", err)
	}
	eb := verImpl.NewEventBuilderFromProtoEvent(proto)
	signedEvent, err := eb.Build(time.Now(), spec.ServerName(s.serverName), s.KeyID, s.Priv)
	if err != nil {
		return nil, fmt.Errorf("EventCreator: failed to sign event: %s", err)
	}
	return signedEvent, nil
}

func (i *ServerRoomImplDefault) PopulateFromSendJoinResponse(room *ServerRoom, joinEvent gomatrixserverlib.PDU, resp fclient.RespSendJoin) {
	stateEvents := resp.StateEvents.UntrustedEvents(room.Version)
	for _, ev := range stateEvents {
		room.ReplaceCurrentState(ev)
	}
	room.AddEvent(joinEvent)
}

func (i *ServerRoomImplDefault) GenerateSendJoinResponse(room *ServerRoom, s *Server, joinEvent gomatrixserverlib.PDU, expectPartialState, omitServersInRoom bool) fclient.RespSendJoin {
	// build the state list *before* we insert the new event
	var stateEvents []gomatrixserverlib.PDU
	room.StateMutex.RLock()
	for _, ev := range room.State {
		// filter out non-critical memberships if this is a partial-state join
		if expectPartialState {
			if ev.Type() == "m.room.member" && ev.StateKey() != joinEvent.StateKey() {
				continue
			}
		}
		stateEvents = append(stateEvents, ev)
	}
	room.StateMutex.RUnlock()

	authEvents := room.AuthChainForEvents(stateEvents)

	// get servers in room *before* the join event
	serversInRoom := []string{s.serverName}
	if !omitServersInRoom {
		serversInRoom = room.ServersInRoom()
	}

	// insert the join event into the room state
	room.AddEvent(joinEvent)
	log.Printf("Received send-join of event %s", joinEvent.EventID())

	// return state and auth chain
	return fclient.RespSendJoin{
		Origin:         spec.ServerName(s.serverName),
		AuthEvents:     gomatrixserverlib.NewEventJSONsFromEvents(authEvents),
		StateEvents:    gomatrixserverlib.NewEventJSONsFromEvents(stateEvents),
		MembersOmitted: expectPartialState,
		ServersInRoom:  serversInRoom,
	}
}
