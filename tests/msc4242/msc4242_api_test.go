package tests

// The tests in this directory test the actual API changes for MSC4242 by using
// a Complement test federation server.
//
// These tests reference codes to exhaustively cover the MSC.
// Some tests are subsequently broken down into 'Inbound' and 'Outbound'
// which represents the direction of the request being tested
// FROM THE PERSPECTIVE OF THE HOMESERVER. They are represented with the code 'IO'.
//
// E.g for /send_join: if the state DAG is valid and connected, accept.
//   SJ01-Out: HS does /send_join. Complement sends back a /send_join response with a valid state DAG.
//             The homeserver does the validation check.
//   SJ00-In:  Complement does /send_join. HS sends back a valid state DAG.
//             Complement does the validation check.
//
// Not all tests have counterparts because we cannot force the HS to send back
// faulty state which will fail validation, so typically only the happy case tests will
// have counterparts. This means tests without counterparts are typically 'Outbound'
// as we can force the Complement response to be faulty, with the notable exception of
// pushed events via /send which injects faulty events.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"testing"

	"github.com/gorilla/mux"
	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/tidwall/gjson"
)

var (
	roomVersion gomatrixserverlib.RoomVersion = "org.matrix.msc4242.12"
	empty                                     = ""
)

type stateDAGimpl struct {
	callback               func(resp fclient.RespSendJoin)
	mutateSendJoinResponse func(resp *fclient.RespSendJoin, joinEvent gomatrixserverlib.PDU)
	dontCheckFwdExtrems    bool
}
type StateDAGRoomOpt func(impl *stateDAGimpl)

// WithCallback is called when Complement joins a state DAG room. It is called with the homeserver's
// /send_join response.
func WithCallback(callback func(resp fclient.RespSendJoin)) StateDAGRoomOpt {
	return func(impl *stateDAGimpl) {
		impl.callback = callback
	}
}

// WithMutateSendJoinResponse is called just before Complement sends a /send_join response back to the homeserver.
// It allows tests to mutate the response e.g adding or removing state_dag events.
func WithMutateSendJoinResponse(mutate func(resp *fclient.RespSendJoin, joinEvent gomatrixserverlib.PDU)) StateDAGRoomOpt {
	return func(impl *stateDAGimpl) {
		impl.mutateSendJoinResponse = mutate
	}
}

// WithDontCheckForwardExtremities if set will not do validation on ForwardExtremities when setting prev_events/prev_state_events.
func WithDontCheckForwardExtremities() StateDAGRoomOpt {
	return func(impl *stateDAGimpl) {
		impl.dontCheckFwdExtrems = true
	}
}

// ServerRoomImplStateDAG makes the room use state DAGs i.e set prev_state_events and generate valid /send_join responses.
func ServerRoomImplStateDAG(t ct.TestLike, srv *federation.Server, opts ...StateDAGRoomOpt) federation.ServerRoomImpl {
	findLastStateEventID := func(room *federation.ServerRoom) string {
		room.TimelineMutex.RLock()
		defer room.TimelineMutex.RUnlock()
		for i := len(room.Timeline) - 1; i >= 0; i-- {
			if room.Timeline[i].StateKey() != nil {
				return room.Timeline[i].EventID()
			}
		}
		t.Logf("%s: failed to find any state event in %d timeline events, no prev_state_events will be set!", room.RoomID, len(room.Timeline))
		return ""
	}
	impl := &stateDAGimpl{}
	for _, opt := range opts {
		opt(impl)
	}
	return &federation.ServerRoomImplCustom{
		ServerRoomImplDefault: federation.ServerRoomImplDefault{},
		ProtoEventCreatorFn: func(def federation.ServerRoomImpl, room *federation.ServerRoom, ev federation.Event) (*gomatrixserverlib.ProtoEvent, error) {
			proto, err := def.ProtoEventCreator(room, ev)
			if err != nil {
				return nil, err
			}
			proto.AuthEvents = nil

			if ev.Type == spec.MRoomCreate && ev.StateKey != nil && *ev.StateKey == "" {
				proto.PrevStateEvents = &[]string{}
			} else {
				// if the fwd extrems are state, use that.
				var fwdExtrems []gomatrixserverlib.PDU
				for _, id := range room.ForwardExtremities {
					pdu, ok := room.GetEventInTimeline(id)
					if ok && pdu.StateKey() != nil {
						fwdExtrems = append(fwdExtrems, pdu)
					}
				}
				if len(fwdExtrems) == 0 {
					proto.PrevStateEvents = &[]string{
						findLastStateEventID(room),
					}
				} else {
					ids := make([]string, len(fwdExtrems))
					for i := range ids {
						ids[i] = fwdExtrems[i].EventID()
					}
					proto.PrevStateEvents = &ids
				}
			}
			if impl.dontCheckFwdExtrems {
				cpy := make([]string, len(room.ForwardExtremities))
				copy(cpy, room.ForwardExtremities)
				proto.PrevStateEvents = &cpy
				proto.PrevEvents = cpy
			}
			return proto, nil
		},
		PopulateFromSendJoinResponseFn: func(def federation.ServerRoomImpl, room *federation.ServerRoom, joinEvent gomatrixserverlib.PDU, resp fclient.RespSendJoin) {
			stateDAGEvents := resp.StateDAG.UntrustedEvents(roomVersion)
			sort.Slice(stateDAGEvents, func(i, j int) bool {
				return stateDAGEvents[i].Depth() < stateDAGEvents[j].Depth()
			})
			// we assume no forks and no rejected events, so we can just bluntly replace events in depth order
			// to work out the current state
			for _, state := range stateDAGEvents {
				room.ReplaceCurrentState(state)
			}
			room.AddEvent(joinEvent)
			if impl.callback != nil {
				impl.callback(resp)
			}
		},
		GenerateSendJoinResponseFn: func(def federation.ServerRoomImpl, room *federation.ServerRoom, s *federation.Server, joinEvent gomatrixserverlib.PDU, expectPartialState, omitServersInRoom bool) fclient.RespSendJoin {
			res := fclient.RespSendJoin{
				ServersInRoom: []string{},
			}
			res.Event = joinEvent.JSON()
			res.MembersOmitted = omitServersInRoom
			for _, ev := range room.Timeline {
				if ev.StateKey() != nil {
					res.StateDAG = append(res.StateDAG, ev.JSON())
				}
			}
			serversInRoom := room.ServersInRoom()
			for _, srvName := range serversInRoom {
				res.ServersInRoom = append(res.ServersInRoom, string(srvName))
			}
			if impl.mutateSendJoinResponse != nil {
				impl.mutateSendJoinResponse(&res, joinEvent)
			}
			room.AddEvent(joinEvent)
			return res
		},
	}
}

type MSC4242Event struct {
	federation.Event
	PrevStateEvents []string
}

func AsPDUs(t ct.TestLike, events []json.RawMessage) []gomatrixserverlib.PDU {
	t.Helper()
	roomVer := gomatrixserverlib.MustGetRoomVersion(roomVersion)
	pdus := make([]gomatrixserverlib.PDU, len(events))
	for i := range events {
		event, err := roomVer.NewEventFromTrustedJSON(events[i], false)
		must.NotError(t, fmt.Sprintf("failed to load event %d", i), err)
		pdus[i] = event
	}
	return pdus
}

func AsEventIDs(t ct.TestLike, events []gomatrixserverlib.PDU) []string {
	t.Helper()
	eventIDs := make([]string, len(events))
	for i := range events {
		eventIDs[i] = events[i].EventID()
	}
	return eventIDs
}

func AsEventJSONs(pdus []gomatrixserverlib.PDU) []json.RawMessage {
	jsons := make([]json.RawMessage, len(pdus))
	for i := range pdus {
		jsons[i] = pdus[i].JSON()
	}
	return jsons
}

// Checks that the StateGraph works
func TestGraph(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		// accept incoming presence transactions, etc
		federation.HandleTransactionRequests(nil, nil),
		// accept incoming /event requests
		federation.HandleEventRequests(),
		federation.HandleMakeSendJoinRequests(),
	)
	cancel := srv.Listen()
	defer cancel()
	bob := srv.UserID("bob")
	room := srv.MustMakeRoom(t, roomVersion,
		federation.InitialRoomEvents(roomVersion, bob),
		federation.WithImpl(ServerRoomImplStateDAG(t, srv)),
	)
	// 100 linear events
	for i := 0; i < 100; i++ {
		ev := srv.MustCreateEvent(t, room, federation.Event{
			Type:     spec.MRoomMember,
			Sender:   bob,
			StateKey: &bob,
			Content: map[string]interface{}{
				"membership":  spec.Join,
				"displayname": fmt.Sprintf("event %d", i),
			},
		})
		room.AddEvent(ev)
	}
	sg := NewStateGraph()
	sg.Update(room.Timeline)
	want := AsEventIDs(t, room.Timeline)
	slices.Reverse(want)
	lastEvent := room.Timeline[len(room.Timeline)-1]
	got := AsEventIDs(t, sg.GetMissingEvents([]string{lastEvent.EventID()}, 1000))
	if !slices.Equal(got, want[1:]) { // we don't return lastEvent
		ct.Fatalf(t, "failed to walk back up the graph:\ngot  %v\nwant %v", got, want[1:])
	}

	// now try forks
	var forkEvents []gomatrixserverlib.PDU
	for i := 0; i < 10; i++ {
		ev := srv.MustCreateEvent(t, room, federation.Event{
			Type:     spec.MRoomMember,
			Sender:   bob,
			StateKey: &bob,
			Content: map[string]interface{}{
				"membership":  spec.Join,
				"displayname": fmt.Sprintf("fork event %d", i),
			},
		})
		forkEvents = append(forkEvents, ev)
	}
	for _, ev := range forkEvents {
		room.AddEvent(ev)
	}
	sg.Update(forkEvents)
	room.ForwardExtremities = AsEventIDs(t, forkEvents)
	merge := srv.MustCreateEvent(t, room, federation.Event{
		Type:     spec.MRoomMember,
		Sender:   bob,
		StateKey: &bob,
		Content: map[string]interface{}{
			"membership":  spec.Join,
			"displayname": "merge",
		},
	})
	room.AddEvent(merge)
	sg.Update([]gomatrixserverlib.PDU{merge})
	// should return in ascii sort order
	want = AsEventIDs(t, forkEvents)
	slices.Sort(want)
	got = AsEventIDs(t, sg.GetMissingEvents([]string{merge.EventID()}, 10))
	if !slices.Equal(got, want) {
		ct.Fatalf(t, "failed to walk back up the fork graph:\ngot  %v\nwant %v", got, want)
	}
}

type Graph struct {
	stateGraph map[string][]string
	eventGraph map[string][]string
	events     map[string]gomatrixserverlib.PDU
	// If true, walks the graph induced by prev_events instead of prev_state_events
	// Used for testing normal /get_missing_events behaviour
	WalkPrevEvents bool
}

func NewStateGraph() *Graph {
	return &Graph{
		stateGraph: make(map[string][]string),
		eventGraph: make(map[string][]string),
		events:     make(map[string]gomatrixserverlib.PDU),
	}
}

func (g *Graph) Update(pdus []gomatrixserverlib.PDU) {
	for _, pdu := range pdus {
		g.stateGraph[pdu.EventID()] = pdu.PrevStateEventIDs()
		g.eventGraph[pdu.EventID()] = pdu.PrevEventIDs()
		g.events[pdu.EventID()] = pdu
	}
}

// GetMissingEvents implements /get_missing_events for the state DAG, including sort ordering
func (g *Graph) GetMissingEvents(from []string, limit int) (result []gomatrixserverlib.PDU) {
	queue := make([]string, len(from))
	seen := make(map[string]bool)
	copy(queue, from)
	slices.Sort(queue)
	for i := 0; i < len(queue); i++ {
		if len(result) >= limit {
			return result
		}
		next := queue[i]
		if seen[next] {
			// we could have N events all with the same prev_state_event
			// and we don't want to then explore that same event N times
			continue
		}
		seen[next] = true
		prevs := g.stateGraph[next]
		slices.Sort(prevs)
		queue = append(queue, prevs...)
		for _, p := range prevs {
			result = append(result, g.events[p])
			if len(result) >= limit {
				return result
			}
		}
	}
	return result
}

// BackwardsExtremities returns the backwards extremities starting from 'from'.
// Backwards extremities are the earliest events known in this graph, so we do not have the complete
// prev_state_events for them.
func (g *Graph) BackwardsExtremities(from []gomatrixserverlib.PDU) []gomatrixserverlib.PDU {
	queue := make([]gomatrixserverlib.PDU, len(from))
	copy(queue, from)
	var result []gomatrixserverlib.PDU
	for i := 0; i < len(queue); i++ {
		event := queue[i]
		// check if we know all prev_state_events
		seenAll := true
		var pses []gomatrixserverlib.PDU
		prevEvents := event.PrevStateEventIDs()
		if g.WalkPrevEvents {
			prevEvents = event.PrevEventIDs()
		}
		for _, pse := range prevEvents {
			prevEv, ok := g.events[pse]
			if !ok {
				seenAll = false
				break
			}
			pses = append(pses, prevEv)
		}

		// if we have seen them all, this event is not a backwards extremity
		// so add its prev_state_events to the queue
		if seenAll {
			queue = append(queue, pses...)
			continue
		}

		// else it is definitely a backwards extremity
		result = append(result, event)
	}
	return result
}

func prevStateEvents(t ct.TestLike, event gjson.Result) []string {
	pae := event.Get("prev_state_events")
	if !pae.Exists() || !pae.IsArray() {
		t.Fatalf("prev_state_events field missing/not an array on event: %s", event.Raw)
	}
	var prevStateEvents []string
	for _, val := range pae.Array() {
		if val.Type != gjson.String {
			t.Fatalf("prev_state_events entries aren't strings: %s", event.Raw)
		}
		prevStateEvents = append(prevStateEvents, val.Str)
	}
	return prevStateEvents
}

func prevStateEventsMatcher(fieldName string, wantprevStateEvents []string) match.JSON {
	iwantprevStateEvents := make([]interface{}, len(wantprevStateEvents))
	for i := range wantprevStateEvents {
		iwantprevStateEvents[i] = wantprevStateEvents[i]
	}
	return match.JSONCheckOff(fieldName, iwantprevStateEvents, match.CheckOffMapper(func(r gjson.Result) interface{} {
		return r.Str
	}))
}

// LoadStateDAG loads all the events under state_dag and puts their event_id: prev_state_events into a map.
// Performs no verification that the DAG is connected.
func LoadStateDAG(t ct.TestLike, sendJoinResp fclient.RespSendJoin) (joinEventID string, stateDAG map[string][]string) {
	roomVer := gomatrixserverlib.MustGetRoomVersion(roomVersion)
	joinEvent, err := roomVer.NewEventFromTrustedJSON(sendJoinResp.Event, false)
	must.NotError(t, "failed to load join event", err)
	stateDAG = make(map[string][]string) // event_id => prev_state_events
	for _, stateEventJSON := range sendJoinResp.StateDAG {
		stateEvent, err := roomVer.NewEventFromTrustedJSON(stateEventJSON, false)
		must.NotError(t, "failed to load event json", err)
		stateEventGJSON := gjson.ParseBytes(stateEventJSON)
		prevStateEvents := prevStateEvents(t, stateEventGJSON)
		stateDAG[stateEvent.EventID()] = prevStateEvents
	}

	// Include the prev state events of the join event itself as that is what we care about
	joinEventPrevStateEvents := prevStateEvents(t, gjson.ParseBytes(sendJoinResp.Event))
	stateDAG[joinEvent.EventID()] = joinEventPrevStateEvents
	return joinEvent.EventID(), stateDAG
}

// We have to implement our own /make|send_join dance here as gmsl is unaware of state DAGs.
// We can reuse some bits though.
func MustJoinRoom(
	t ct.TestLike, s *federation.Server, deployment federation.FederationDeployment,
	remoteServer spec.ServerName, roomID string, userID string, opts ...federation.JoinRoomOpt,
) (*federation.ServerRoom, fclient.RespSendJoin) {
	t.Helper()
	var result fclient.RespSendJoin
	opts = append(opts, federation.WithRoomOpts(
		federation.WithImpl(
			ServerRoomImplStateDAG(
				t, s, WithCallback(func(resp fclient.RespSendJoin) {
					result = resp
				}),
			),
		),
	))
	room := s.MustJoinRoom(t, deployment, remoteServer, roomID, userID, opts...)
	return room, result
}

func generateDisplayNameChanges(t ct.TestLike, srv *federation.Server, room *federation.ServerRoom, userID string, numMsgs int, addEventsToRoom bool) (eventsToSend []gomatrixserverlib.PDU) {
	for i := 0; i < numMsgs; i++ {
		pdu := srv.MustCreateEvent(t, room, federation.Event{
			Type:     spec.MRoomMember,
			Sender:   userID,
			StateKey: &userID,
			Content: map[string]interface{}{
				"membership":  spec.Join,
				"displayname": fmt.Sprintf("%s %d", userID, i),
			},
		})
		if addEventsToRoom {
			room.AddEvent(pdu)
		}
		eventsToSend = append(eventsToSend, pdu)
	}
	return eventsToSend
}

func extractGetMissingEventsRequest(forRoomID string, req *http.Request) (*fclient.MissingEvents, error) {
	vars := mux.Vars(req)
	roomID := vars["roomID"]
	if roomID != forRoomID {
		return nil, fmt.Errorf(`{"error":"complement: /get_missing_events for wrong room: %s != %s"}`, roomID, forRoomID)
	}
	var body fclient.MissingEvents
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf(`{"error":"complement: cannot parse request body"}`)
	}
	return &body, nil
}

func backfill(t ct.TestLike, cli *client.CSAPI, roomVersion gomatrixserverlib.RoomVersion, roomID, prevBatch string, limit int) []gomatrixserverlib.PDU {
	t.Helper()
	res := cli.MustDo(t, "GET", []string{
		"_matrix", "client", "v3", "rooms", roomID, "messages",
	}, client.WithQueries(url.Values{
		"dir":   []string{"b"},
		"from":  []string{prevBatch},
		"limit": []string{strconv.Itoa(limit)},
	}))
	resp := struct {
		Chunk gomatrixserverlib.EventJSONs `json:"chunk"`
	}{}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		ct.Errorf(t, "failed to decode /message response: %v", err)
		return nil
	}
	return resp.Chunk.TrustedEvents(roomVersion, false)
}

func mustCreateEvent(t ct.TestLike, s *federation.Server, room *federation.ServerRoom, ev MSC4242Event) gomatrixserverlib.PDU {
	t.Helper()
	content, err := json.Marshal(ev.Content)
	if err != nil {
		ct.Fatalf(t, "MustCreateEvent: failed to marshal event content %s - %+v", err, ev.Content)
	}
	var unsigned []byte
	if ev.Unsigned != nil {
		unsigned, err = json.Marshal(ev.Unsigned)
		if err != nil {
			ct.Fatalf(t, "MustCreateEvent: failed to marshal event unsigned: %s - %+v", err, ev.Unsigned)
		}
	}

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
		SenderID:        ev.Sender,
		Depth:           int64(room.Depth + 1), // depth starts at 1
		Type:            ev.Type,
		StateKey:        ev.StateKey,
		Content:         content,
		RoomID:          room.RoomID,
		PrevEvents:      prevEvents,
		Unsigned:        unsigned,
		Redacts:         ev.Redacts,
		PrevStateEvents: &ev.PrevStateEvents,
	}
	if proto.AuthEvents == nil {
		var stateNeeded gomatrixserverlib.StateNeeded
		stateNeeded, err = gomatrixserverlib.StateNeededForProtoEvent(&proto)
		if err != nil {
			ct.Fatalf(t, "MustCreateEvent: failed to work out auth_events : %s", err)
		}
		proto.AuthEvents = room.AuthEvents(stateNeeded)
	}

	signedEvent, err := room.EventCreator(room, s, &proto)
	if err != nil {
		ct.Fatalf(t, "MustCreateEvent: failed to create event: %s", err)
	}
	return signedEvent
}
