package client

import (
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// StateStore implements the StateStore interface for OlmMachine.
// It is used to determine which rooms are encrypted and which rooms are shared with a user.
// The state is updated by /sync responses.
type StateStore struct {
	storer *mautrix.InMemoryStore
}

// GetEncryptionEvent returns the encryption event for a room.
func (ss *StateStore) GetEncryptionEvent(roomID id.RoomID) *event.EncryptionEventContent {
	room := ss.storer.LoadRoom(roomID)
	if room == nil {
		return nil
	}
	if evts, ok := room.State[event.StateEncryption]; ok {
		if evt, ok := evts[""]; ok {
			return evt.Content.AsEncryption()
		}
	}
	return nil
}

// IsEncrypted returns whether a room has been encrypted.
func (ss *StateStore) IsEncrypted(roomID id.RoomID) bool {
	room := ss.storer.LoadRoom(roomID)
	if room == nil {
		return false
	}
	_, ok := room.State[event.StateEncryption]
	return ok
}

// FindSharedRooms returns a list of room IDs that the given user ID is also a member of.
func (ss *StateStore) FindSharedRooms(userID id.UserID) []id.RoomID {
	sharedRooms := make([]id.RoomID, 0)
	for roomID, room := range ss.storer.Rooms {
		if room.GetMembershipState(userID) == event.MembershipJoin {
			sharedRooms = append(sharedRooms, roomID)
		}
	}
	return sharedRooms
}

// UpdateStateStore updates the internal state of StateStore from a /sync response.
func (ss *StateStore) UpdateStateStore(resp *mautrix.RespSync) {
	for roomID, evts := range resp.Rooms.Join {
		room := ss.storer.LoadRoom(roomID)
		if room == nil {
			room = mautrix.NewRoom(roomID)
			ss.storer.SaveRoom(room)
		}
		for _, i := range evts.State.Events {
			room.UpdateState(i)
		}
		for _, i := range evts.Timeline.Events {
			if i.Type.IsState() {
				room.UpdateState(i)
			}
		}
	}
}
