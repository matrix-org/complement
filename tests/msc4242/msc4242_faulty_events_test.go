package tests

import (
	"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
)

type FaultyEventTestCase struct {
	Name           string
	CodeSuffix     string
	GenerateEvents func(t ct.TestLike, srv *federation.Server, room *federation.ServerRoom, sender string) []gomatrixserverlib.PDU
}

// arbitrary dummy state event type for tests
const faultyStateEventType = "faulty.state.event"

// faultyEventTestCases are all the ways you can create faulty events.
// When GenerateEvents is called, the room must be a normal room with the initial creation events only,
// as the generator makes various assumptions e.g that the sender is joined, that there are join rules, etc.
var faultyEventTestCases = []FaultyEventTestCase{
	{
		Name:       "Missing all prev_state_events on an event",
		CodeSuffix: "A",
		GenerateEvents: func(t ct.TestLike, srv *federation.Server, room *federation.ServerRoom, sender string) []gomatrixserverlib.PDU {
			return []gomatrixserverlib.PDU{
				mustCreateEvent(t, srv, room, MSC4242Event{
					Event: federation.Event{
						Type:     faultyStateEventType,
						Sender:   sender,
						StateKey: &empty,
						Content: map[string]interface{}{
							"name": "This event is missing all prev_state_events so should be rejected",
						},
						PrevEvents: []string{room.CurrentState(spec.MRoomMember, sender).EventID()},
					},
					// no event in the state DAG has this event ID.
					PrevStateEvents: []string{"$4PRgaFIMcD9z4vzgkUUm0YI5CZHYORUPzWGJac6guAo"},
				}),
			}
		},
	},
	{
		Name:       "Missing partial prev_state_events on an event",
		CodeSuffix: "B",
		GenerateEvents: func(t ct.TestLike, srv *federation.Server, room *federation.ServerRoom, sender string) []gomatrixserverlib.PDU {
			return []gomatrixserverlib.PDU{
				mustCreateEvent(t, srv, room, MSC4242Event{
					Event: federation.Event{
						Type:     faultyStateEventType,
						Sender:   sender,
						StateKey: &empty,
						Content: map[string]interface{}{
							"name": "This event is missing all prev_state_events so should be rejected",
						},
						PrevEvents: []string{room.CurrentState(spec.MRoomMember, sender).EventID()},
					},
					// some of the event IDs exist in the state DAG but not all.
					PrevStateEvents: []string{
						room.CurrentState(spec.MRoomJoinRules, "").EventID(),
						"$4PRgaFIMcD9z4vzgkUUm0YI5CZHYORUPzWGJac6guAo",
					},
				}),
			}
		},
	},
	{
		Name:       "Referencing message events in prev_state_events on an event",
		CodeSuffix: "C",
		GenerateEvents: func(t ct.TestLike, srv *federation.Server, room *federation.ServerRoom, sender string) []gomatrixserverlib.PDU {
			msg := mustCreateEvent(t, srv, room, MSC4242Event{
				Event: federation.Event{
					Type:   "m.room.message",
					Sender: sender,
					Content: map[string]interface{}{
						"body":    "I am a message",
						"msgtype": "m.text",
					},
				},
				PrevStateEvents: []string{
					room.CurrentState(spec.MRoomJoinRules, "").EventID(),
				},
			})
			return []gomatrixserverlib.PDU{
				msg, mustCreateEvent(t, srv, room, MSC4242Event{
					Event: federation.Event{
						Type:     faultyStateEventType,
						Sender:   sender,
						StateKey: &empty,
						Content: map[string]interface{}{
							"info": "I should be rejected as I reference a message event in prev_state_events",
						},
					},
					PrevStateEvents: []string{msg.EventID()},
				}),
			}
		},
	},
	{
		Name:       "Referencing message events in addition to correct events in prev_state_events on an event",
		CodeSuffix: "D",
		GenerateEvents: func(t ct.TestLike, srv *federation.Server, room *federation.ServerRoom, sender string) []gomatrixserverlib.PDU {
			msg := mustCreateEvent(t, srv, room, MSC4242Event{
				Event: federation.Event{
					Type:   "m.room.message",
					Sender: sender,
					Content: map[string]interface{}{
						"body":    "I am a message",
						"msgtype": "m.text",
					},
				},
				PrevStateEvents: []string{
					room.CurrentState(spec.MRoomJoinRules, "").EventID(),
				},
			})
			return []gomatrixserverlib.PDU{
				msg, mustCreateEvent(t, srv, room, MSC4242Event{
					Event: federation.Event{
						Type:     faultyStateEventType,
						Sender:   sender,
						StateKey: &empty,
						Content: map[string]interface{}{
							"info": "I should be rejected as I reference a message event in prev_state_events",
						},
					},
					PrevStateEvents: []string{
						room.CurrentState(spec.MRoomJoinRules, "").EventID(),
						msg.EventID(),
					},
				}),
			}
		},
	},
	{
		Name:       "The event fails auth checks (charlie is not joined)",
		CodeSuffix: "E",
		GenerateEvents: func(t ct.TestLike, srv *federation.Server, room *federation.ServerRoom, sender string) []gomatrixserverlib.PDU {
			charlie := srv.UserID("charlie")
			return []gomatrixserverlib.PDU{
				mustCreateEvent(t, srv, room, MSC4242Event{
					Event: federation.Event{
						Type:     faultyStateEventType,
						Sender:   charlie,
						StateKey: &empty,
						Content: map[string]interface{}{
							"info": "I should be rejected because charlie is not in the room",
						},
					},
					PrevStateEvents: []string{
						room.CurrentState(spec.MRoomJoinRules, "").EventID(),
					},
				}),
			}
		},
	},
	{
		Name:       "Zero prev_state_events",
		CodeSuffix: "F",
		GenerateEvents: func(t ct.TestLike, srv *federation.Server, room *federation.ServerRoom, sender string) []gomatrixserverlib.PDU {
			return []gomatrixserverlib.PDU{
				mustCreateEvent(t, srv, room, MSC4242Event{
					Event: federation.Event{
						Type:     faultyStateEventType,
						Sender:   sender,
						StateKey: &empty,
						Content: map[string]interface{}{
							"info": "I should be rejected as I have no prev_state_events",
						},
					},
					PrevStateEvents: []string{},
				}),
			}
		},
	},
	{
		Name:       "valid event references rejected event causes the valid event to be rejected",
		CodeSuffix: "H",
		GenerateEvents: func(t ct.TestLike, srv *federation.Server, room *federation.ServerRoom, sender string) []gomatrixserverlib.PDU {
			charlie := srv.UserID("charlie")
			doris := srv.UserID("doris")
			rejectedEvent := mustCreateEvent(t, srv, room, MSC4242Event{
				Event: federation.Event{
					Type:     spec.MRoomName,
					Sender:   doris,
					StateKey: &empty,
					Content: map[string]interface{}{
						"name": "Doris is not joined so cannot set the room name, so this event is rejected.",
					},
				},
				PrevStateEvents: []string{
					room.CurrentState(spec.MRoomJoinRules, "").EventID(),
				},
			})
			// valid event but references a rejected event which is a no-no
			charlieJoin := mustCreateEvent(t, srv, room, MSC4242Event{
				Event: federation.Event{
					Type:     spec.MRoomMember,
					Sender:   charlie,
					StateKey: &charlie,
					Content: map[string]interface{}{
						"membership": "join",
					},
				},
				PrevStateEvents: []string{rejectedEvent.EventID()},
			})
			// This event is also valid and even references another valid event but
			// it is rejected because it references rejected events 2 events ago.
			bobName := mustCreateEvent(t, srv, room, MSC4242Event{
				Event: federation.Event{
					Type:     spec.MRoomName,
					Sender:   sender,
					StateKey: &empty,
					Content: map[string]interface{}{
						"name": "This event is rejected because it references rejected events 2 events ago",
					},
				},
				PrevStateEvents: []string{charlieJoin.EventID()},
			})

			return []gomatrixserverlib.PDU{
				rejectedEvent, charlieJoin, bobName,
			}
		},
	},
}
