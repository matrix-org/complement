package conversation

import (
	"fmt"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/tidwall/gjson"
)

// An Exchange is a single exchange of data from one client to another client in a conversation.
type Exchange interface {
	// Emit the data from a client, blocks until the data has been sent.
	Do(t *testing.T, speaker *client.CSAPI)
	// Listen blocks until the data has been received on the `listener`
	// This function may be called multiple times concurrently, but always after Do() has returned.
	Listen(t *testing.T, listener *client.CSAPI)
}

// Exchange an event in a room
type ExchangeEvent struct {
	RoomID  string
	Event   b.Event
	eventID string
}

func (e *ExchangeEvent) Do(t *testing.T, speaker *client.CSAPI) {
	fmt.Printf("ExchangeEvent.Do %s %s \n", e.RoomID, speaker.UserID)
	e.eventID = speaker.SendEventSynced(t, e.RoomID, e.Event)
}

func (e *ExchangeEvent) Listen(t *testing.T, listener *client.CSAPI) {
	fmt.Printf("ExchangeEvent.Listen %s %s %s\n", e.RoomID, listener.UserID, e.eventID)
	listener.SyncUntilTimelineHas(t, e.RoomID, func(r gjson.Result) bool {
		return r.Get("event_id").Str == e.eventID
	})
}

// Exchange a membership change
type ExchangeMembership struct {
	RoomID      string
	ServerNames []string
	Membership  string
	Target      string
	eventID     string
}

func (e *ExchangeMembership) Do(t *testing.T, speaker *client.CSAPI) {
	if e.Target == "" {
		e.Target = speaker.UserID
	}
	t.Logf("ExchangeMembership: %s is changing the membership of %s in room %s to %s\n", speaker.UserID, e.Target, e.RoomID, e.Membership)
	switch e.Membership {
	case "join":
		speaker.JoinRoom(t, e.RoomID, e.ServerNames)
	case "leave":
		speaker.LeaveRoom(t, e.RoomID)
	case "invite":
		speaker.InviteRoom(t, e.RoomID, e.Target)
	default:
		t.Fatalf("ExchangeMembership: invalid Membership: %s", e.Membership)
	}
}

func (e *ExchangeMembership) Listen(t *testing.T, listener *client.CSAPI) {
	t.Logf("ExchangeMembership: %s is listening for %s to '%s' room %s\n", listener.UserID, e.Target, e.Membership, e.RoomID)
	listener.SyncUntilTimelineHas(t, e.RoomID, func(r gjson.Result) bool {
		return r.Get("type").Str == "m.room.member" && r.Get("state_key").Str == e.Target && r.Get("content.membership").Str == e.Membership
	})
}
