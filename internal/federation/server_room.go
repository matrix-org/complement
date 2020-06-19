package federation

import (
	"encoding/json"
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

// AllCurrentState returns all the current state events
func (r *ServerRoom) AllCurrentState() (events []gomatrixserverlib.Event) {
	for _, ev := range r.State {
		events = append(events, *ev)
	}
	return
}

// AuthChain returns all auth events for all events in the current state TODO: recursively
func (r *ServerRoom) AuthChain() (chain []gomatrixserverlib.Event) {
	chainMap := make(map[string]bool)
	// get all the auth event IDs
	for _, ev := range r.AllCurrentState() {
		for _, evID := range ev.AuthEventIDs() {
			if chainMap[evID] {
				continue
			}
			chainMap[evID] = true
		}
	}
	// find them in the timeline
	for _, tev := range r.Timeline {
		if chainMap[tev.EventID()] {
			chain = append(chain, *tev)
		}
	}
	return
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
func InitialRoomEvents(roomVer gomatrixserverlib.RoomVersion, creator string) []b.Event {
	// need to serialise/deserialise to get map[string]interface{} annoyingly
	plContent := initialPowerLevelsContent(creator)
	plBytes, _ := json.Marshal(plContent)
	var plContentMap map[string]interface{}
	json.Unmarshal(plBytes, &plContentMap)
	return []b.Event{
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
