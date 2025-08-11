package tests

import (
	"fmt"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/gomatrixserverlib"
)

const roomVersion12 = "12"

var V12ServerRoom = federation.ServerRoomImplCustom{
	ProtoEventCreatorFn: ProtoEventCreator,
}

func TestMain(m *testing.M) {
	complement.TestMain(m, "v12")
}

// Override how Complement makes proto events so we can conditionally disable/enable the inclusion of the create event
// depending on whether we're running in combined mode or not.
// Complement also doesn't set the room version correctly on the ProtoEvent as this was a new addition to GMSL.
func ProtoEventCreator(def federation.ServerRoomImpl, room *federation.ServerRoom, ev federation.Event) (*gomatrixserverlib.ProtoEvent, error) {
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
		Version:    gomatrixserverlib.MustGetRoomVersion(room.Version),
	}
	if err := proto.SetContent(ev.Content); err != nil {
		return nil, fmt.Errorf("EventCreator: failed to marshal event content: %s - %+v", err, ev.Content)
	}
	if err := proto.SetUnsigned(ev.Content); err != nil {
		return nil, fmt.Errorf("EventCreator: failed to marshal event unsigned: %s - %+v", err, ev.Unsigned)
	}
	if proto.AuthEvents == nil {
		var stateNeeded gomatrixserverlib.StateNeeded
		// this does the right thing for v12
		stateNeeded, err := gomatrixserverlib.StateNeededForProtoEvent(&proto)
		if err != nil {
			return nil, fmt.Errorf("EventCreator: failed to work out auth_events : %s", err)
		}
		// we never include the create event if the HS supports MSC4291
		stateNeeded.Create = false
		proto.AuthEvents = room.AuthEvents(stateNeeded)
	}
	return &proto, nil
}
