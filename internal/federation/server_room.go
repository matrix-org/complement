package federation

import (
	"fmt"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/gomatrixserverlib"
)

// ServerRoom represents a room on this test federation server
type ServerRoom struct {
	Version  gomatrixserverlib.RoomVersion
	RoomID   string
	State    map[string]*gomatrixserverlib.Event
	Timeline []*gomatrixserverlib.Event
}

// AddEvent adds a new event to the timeline, updating current state if it is a state event.
func (r *ServerRoom) AddEvent(ev *gomatrixserverlib.Event) {
	if ev.StateKey() != nil {
		r.replaceCurrentState(ev)
	}
	r.Timeline = append(r.Timeline, ev)
}

// AuthEvents returns the state event IDs of the auth events which authenticate this event
func (r *ServerRoom) AuthEvents(sn gomatrixserverlib.StateNeeded) (eventIDs []string) {
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

// replaceCurrentState inserts a new state event for this room or replaces current state depending
// on the (type, state_key) provided.
func (r *ServerRoom) replaceCurrentState(ev *gomatrixserverlib.Event) {
	tuple := fmt.Sprintf("%s\x1f%s", ev.Type(), *ev.StateKey())
	r.State[tuple] = ev
}

// CurrentState returns the state event for the given (type, state_key) or nil.
func (r *ServerRoom) CurrentState(evType, stateKey string) *gomatrixserverlib.Event {
	tuple := fmt.Sprintf("%s\x1f%s", evType, stateKey)
	return r.State[tuple]
}

// InitialRoomEvents returns the initial set of events that get created when making a room.
func InitialRoomEvents(creator string) []b.Event {
	plContent := gomatrixserverlib.PowerLevelContent{}
	plContent.Defaults()
	plContent.Users = make(map[string]int64)
	plContent.Users[creator] = 100
	return []b.Event{
		{
			Type:     "m.room.create",
			StateKey: b.Ptr(""),
			Sender:   creator,
			Content:  map[string]interface{}{},
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
			Content: map[string]interface{}{
				"state_default":  plContent.StateDefault,
				"users_default":  plContent.UsersDefault,
				"events_default": plContent.EventsDefault,
				"ban":            plContent.Ban,
				"invite":         plContent.Invite,
				"kick":           plContent.Kick,
				"redact":         plContent.Redact,
				"users":          plContent.Users,
				"events":         plContent.Events,
			},
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
