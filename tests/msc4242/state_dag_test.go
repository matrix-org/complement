package tests

// Test harness for MSC4242 (State DAGs)

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/federation"
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

// ServerRoomImplStateDAG makes the room use state DAGs i.e set prev_state_events and generate valid
// /send_join responses.
func ServerRoomImplStateDAG(t ct.TestLike) federation.ServerRoomImpl {
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
			return proto, nil
		},
		PopulateFromSendJoinResponseFn: func(def federation.ServerRoomImpl, room *federation.ServerRoom, joinEvent gomatrixserverlib.PDU, resp fclient.RespSendJoin) {
			stateDAGEvents := resp.StateDAG.UntrustedEvents(roomVersion)
			sort.Slice(stateDAGEvents, func(i, j int) bool {
				return stateDAGEvents[i].Depth() < stateDAGEvents[j].Depth()
			})
			// we assume no forks and no rejected events, so we can just bluntly replace events in
			// depth order to work out the current state
			for _, state := range stateDAGEvents {
				room.ReplaceCurrentState(state)
			}
			room.AddEvent(joinEvent)
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
			for _, srvName := range room.ServersInRoom() {
				res.ServersInRoom = append(res.ServersInRoom, string(srvName))
			}
			room.AddEvent(joinEvent)
			return res
		},
	}
}

// MSC4242Event is a federation.Event with explicitly set prev_state_events. Use this when you need
// to control the shape of the state DAG, e.g to create forks. Events made with
// Server.MustCreateEvent instead reference the room's current state DAG extremities.
type MSC4242Event struct {
	federation.Event
	PrevStateEvents []string
}

// mustCreateEvent creates and signs an event with explicitly set prev_state_events. It does not add
// the event to the room: see ServerRoom.AddEvent for that.
func mustCreateEvent(t ct.TestLike, s *federation.Server, room *federation.ServerRoom, ev MSC4242Event) gomatrixserverlib.PDU {
	t.Helper()
	content, err := json.Marshal(ev.Content)
	if err != nil {
		ct.Fatalf(t, "mustCreateEvent: failed to marshal event content %s - %+v", err, ev.Content)
	}
	var unsigned []byte
	if ev.Unsigned != nil {
		unsigned, err = json.Marshal(ev.Unsigned)
		if err != nil {
			ct.Fatalf(t, "mustCreateEvent: failed to marshal event unsigned: %s - %+v", err, ev.Unsigned)
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
	// auth_events do not exist on state DAG events: they are calculated from prev_state_events by
	// the receiving server, and the event builder strips the field.
	signedEvent, err := room.EventCreator(room, s, &proto)
	if err != nil {
		ct.Fatalf(t, "mustCreateEvent: failed to create event: %s", err)
	}
	return signedEvent
}

// mustAddStateEvent creates a state event whose prev_state_events are the room's current state DAG
// extremities, then adds it to the room, making it the sole extremity.
func mustAddStateEvent(t *testing.T, srv *federation.Server, room *federation.ServerRoom, ev federation.Event) gomatrixserverlib.PDU {
	t.Helper()
	pdu := srv.MustCreateEvent(t, room, ev)
	room.AddEvent(pdu)
	return pdu
}

// awaitMembership blocks until the Complement server has been told about a membership change for
// userID via a /send transaction, returning the membership event.
//
// Homeservers create leave events for their own joined users locally and federate them in a
// transaction, rather than calling /make_leave, so we have to wait for the event to arrive before
// building on top of it.
func awaitMembership(t *testing.T, room *federation.ServerRoom, userID, membership string) gomatrixserverlib.PDU {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		ev := room.CurrentState(spec.MRoomMember, userID)
		if ev != nil && gjson.GetBytes(ev.Content(), "membership").Str == membership {
			return ev
		}
		if time.Now().After(deadline) {
			got := "<missing>"
			if ev != nil {
				got = gjson.GetBytes(ev.Content(), "membership").Str
			}
			ct.Fatalf(t, "awaitMembership: timed out waiting for %s to be '%s' in %s, got '%s'",
				userID, membership, room.RoomID, got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// currentRoomState returns the current room state as seen by the client, keyed off (type, state_key).
func currentRoomState(t *testing.T, cli *client.CSAPI, roomID string) map[[2]string]gjson.Result {
	t.Helper()
	res := cli.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state"})
	state := make(map[[2]string]gjson.Result)
	for _, ev := range must.ParseJSON(t, res.Body).Array() {
		state[[2]string{ev.Get("type").Str, ev.Get("state_key").Str}] = ev
	}
	return state
}

// mustNotHaveStateEvent fails the test if the given state tuple is present in the room state.
func mustNotHaveStateEvent(t *testing.T, state map[[2]string]gjson.Result, evType, stateKey, reason string) {
	t.Helper()
	if ev, ok := state[[2]string{evType, stateKey}]; ok {
		ct.Fatalf(t, "room state unexpectedly contains (%s, %s): %s\n%s\n%s",
			evType, stateKey, reason, ev.Raw, formatState(state))
	}
}

// mustHaveStateEventContent fails the test unless the given state tuple is present and the given
// content field matches.
func mustHaveStateEventContent(t *testing.T, state map[[2]string]gjson.Result, evType, stateKey, field, want, reason string) {
	t.Helper()
	ev, ok := state[[2]string{evType, stateKey}]
	if !ok {
		ct.Fatalf(t, "room state is missing (%s, %s): %s\n%s", evType, stateKey, reason, formatState(state))
	}
	got := ev.Get("content." + field).Str
	if got != want {
		ct.Fatalf(t, "room state (%s, %s) content.%s: got '%s' want '%s': %s\n%s",
			evType, stateKey, field, got, want, reason, formatState(state))
	}
}

// formatState renders the whole room state so a failed assertion shows what the server actually
// resolved, not just the tuple which was checked.
func formatState(state map[[2]string]gjson.Result) string {
	keys := make([][2]string, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	var sb strings.Builder
	sb.WriteString("full room state as the server resolved it:\n")
	for _, k := range keys {
		ev := state[k]
		fmt.Fprintf(&sb, "  (%s, %s) %s content=%s\n",
			k[0], k[1], ev.Get("event_id").Str, ev.Get("content").Raw)
	}
	return sb.String()
}
