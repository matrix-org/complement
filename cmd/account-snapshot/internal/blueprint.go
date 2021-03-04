package internal

import (
	"encoding/json"
	"log"

	"github.com/matrix-org/complement/internal/b"
	"github.com/tidwall/gjson"
)

// ConvertToBlueprint converts a /sync snapshot to a Complement blueprint
func ConvertToBlueprint(s *Snapshot, serverName string) (*b.Blueprint, error) {
	bp := &b.Blueprint{
		Name: "snapshot_" + s.UserID,
	}
	// TODO: the snapshot has information on servers but we only want 1 server for now
	hs := b.Homeserver{
		Name: serverName,
	}
	log.Printf("Blueprint: creating users (%d)\n", len(s.Devices))
	// Create all the users (and devices) in the snapshot
	for userID, devices := range s.Devices {
		local, _ := split(userID)
		for i := range devices {
			user := b.User{
				Localpart:   local,
				DisplayName: local,
				DeviceID:    &devices[i],
			}
			// set up OTKs if this user+device sends E2E messages
			if devices[i] != NoEncryptedDevice {
				user.OneTimeKeys = 100
			}
			// set DM list if this is the syncing user
			if userID == s.UserID {
				user.AccountData = append(user.AccountData, b.AccountData{
					Type: "m.direct",
					Value: map[string]interface{}{
						"content": s.AccountDataDMs,
					},
				})
			}
			hs.Users = append(hs.Users, user)
		}
	}
	log.Printf("Blueprint: creating rooms (%d)\n", len(s.Rooms))
	for i := range s.Rooms {
		room := convertRoom(&s.Rooms[i])
		if room == nil {
			log.Printf("  skipping room %v\n", s.Rooms[i].ID)
			continue
		}
		hs.Rooms = append(hs.Rooms, *room)
	}
	bp.Homeservers = append(bp.Homeservers, hs)

	return bp, nil
}

func convertRoom(sr *AnonSnapshotRoom) *b.Room {
	r := &b.Room{
		Ref:     sr.ID,
		Creator: sr.Creator,
	}
	// find and set the create content and memberships
	memberships := map[string][]string{}
	otherState := []json.RawMessage{}
	for _, ev := range sr.State {
		switch gjson.GetBytes(ev, "type").Str {
		case "m.room.create":
			var createContent map[string]interface{}
			if err := json.Unmarshal([]byte(gjson.GetBytes(ev, "content").Raw), &createContent); err != nil {
				log.Printf("  cannot convert room, cannot unmarshal m.room.create content: %s\n", err)
				return nil
			}
			r.CreateRoom = createContent
		case "m.room.member":
			membership := gjson.GetBytes(ev, "content.membership").Str
			userID := gjson.GetBytes(ev, "state_key").Str
			memberships[membership] = append(memberships[membership], userID)
		default:
			otherState = append(otherState, ev)
		}
	}
	// Make the room publicly joinable then join all the users in the room
	r.Events = append(r.Events, b.Event{
		Sender:   sr.Creator,
		Type:     "m.room.join_rules",
		StateKey: b.Ptr(""),
		Content: map[string]interface{}{
			"join_rule": "public",
		},
	})
	// All joined users should be joined
	for _, userID := range memberships["join"] {
		r.Events = append(r.Events, b.Event{
			Sender:   userID,
			StateKey: b.Ptr(userID),
			Type:     "m.room.member",
			Content: map[string]interface{}{
				"membership": "join",
			},
		})
	}
	// All left users should join then immediately leave
	for _, userID := range memberships["leave"] {
		r.Events = append(r.Events, b.Event{
			Sender:   userID,
			StateKey: b.Ptr(userID),
			Type:     "m.room.member",
			Content: map[string]interface{}{
				"membership": "join",
			},
		})
		r.Events = append(r.Events, b.Event{
			Sender:   userID,
			StateKey: b.Ptr(userID),
			Type:     "m.room.member",
			Content: map[string]interface{}{
				"membership": "leave",
			},
		})
	}
	// All invited users should be invited, we'll just invite them from the first joined user for now
	for _, userID := range memberships["invite"] {
		r.Events = append(r.Events, b.Event{
			Sender:   memberships["join"][0],
			StateKey: b.Ptr(userID),
			Type:     "m.room.member",
			Content: map[string]interface{}{
				"membership": "invite",
			},
		})
	}
	// All banned users should be banned, we'll ban them from the first joined user for now
	for _, userID := range memberships["ban"] {
		r.Events = append(r.Events, b.Event{
			Sender:   memberships["join"][0],
			StateKey: b.Ptr(userID),
			Type:     "m.room.member",
			Content: map[string]interface{}{
				"membership": "ban",
			},
		})
	}
	// Set all the /sync State (excluding create/member events)
	for _, ev := range otherState {
		r.Events = append(r.Events, b.Event{
			Sender:   gjson.GetBytes(ev, "sender").Str,
			StateKey: b.Ptr(gjson.GetBytes(ev, "state_key").Str),
			Type:     gjson.GetBytes(ev, "type").Str,
			Content:  jsonObject([]byte(gjson.GetBytes(ev, "content").Raw)),
		})
	}

	// roll forward Timeline
	for _, ev := range sr.Timeline {
		var sk *string
		skg := gjson.GetBytes(ev, "state_key")
		if skg.Exists() {
			sk = &skg.Str
		}
		r.Events = append(r.Events, b.Event{
			Sender:   gjson.GetBytes(ev, "sender").Str,
			StateKey: sk,
			Type:     gjson.GetBytes(ev, "type").Str,
			Content:  jsonObject([]byte(gjson.GetBytes(ev, "content").Raw)),
		})
	}

	return r
}

func jsonObject(in json.RawMessage) (out map[string]interface{}) {
	_ = json.Unmarshal(in, &out)
	return
}
