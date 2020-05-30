package federation

import (
	"fmt"

	"github.com/matrix-org/gomatrixserverlib"
)

// ServerRoom represents a room on this test federation server
type ServerRoom struct {
	Version gomatrixserverlib.RoomVersion
	RoomID  string
	State   map[string]*gomatrixserverlib.Event
}

// AuthEvents returns the state event IDs of the auth events which authenticate this event
func (r *ServerRoom) AuthEvents(sn gomatrixserverlib.StateNeeded) (eventIDs []string) {
	if sn.Create {
		eventIDs = append(eventIDs, r.CurrentState("m.room.create", "").EventID())
	}
	if sn.JoinRules {
		eventIDs = append(eventIDs, r.CurrentState("m.room.join_rules", "").EventID())
	}
	if sn.PowerLevels {
		eventIDs = append(eventIDs, r.CurrentState("m.room.power_levels", "").EventID())
	}
	for _, mem := range sn.Member {
		eventIDs = append(eventIDs, r.CurrentState("m.room.member", mem).EventID())
	}
	return
}

// ReplaceCurrentState inserts a new state event for this room or replaces current state depending
// on the (type, state_key) provided.
func (r *ServerRoom) ReplaceCurrentState(ev *gomatrixserverlib.Event) {
	if ev.StateKey() == nil {
		panic(r.RoomID + " : AddNewStateEvent event is not a state event")
	}
	tuple := fmt.Sprintf("%s\x1f%s", ev.Type(), *ev.StateKey())
	r.State[tuple] = ev
}

// CurrentState returns the state event for the given (type, state_key) or nil.
func (r *ServerRoom) CurrentState(evType, stateKey string) *gomatrixserverlib.Event {
	tuple := fmt.Sprintf("%s\x1f%s", evType, stateKey)
	return r.State[tuple]
}
