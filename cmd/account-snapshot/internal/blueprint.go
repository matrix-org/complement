package internal

import (
	"encoding/json"
	"log"
	"regexp"

	"github.com/matrix-org/complement/internal/b"
	"github.com/tidwall/gjson"
)

var ignoredEventType = map[string]bool{
	"m.room.tombstone": true, // TODO: need to hit /upgrade API
	"m.room.encrypted": true, // TODO: need to be able to send E2E messages and then give keys to homerunner clients
	"m.reaction":       true, // TODO: uhh not in spec, needs event_id mapping
	"m.room.redaction": true, // TODO: Hit /redact API, needs event_id mapping first
}
var regexpAlphanums = regexp.MustCompile("[^a-zA-Z0-9]+")

// ConvertToBlueprint converts a /sync snapshot to a Complement blueprint
func ConvertToBlueprint(s *Snapshot, serverName string) (*b.Blueprint, error) {
	bp := &b.Blueprint{
		// format that Docker images are happy with
		Name: "snapshot_" + regexpAlphanums.ReplaceAllString(s.UserID, ""),
		// only keep the access token for the user whose account is being snapshotted else
		// we could persist 10,000s of tokens as labels and make Docker sad
		KeepAccessTokensForUsers: []string{s.UserID},
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

	// remove users who have left and done nothing else of consequence
	removeUnusedUsers(&hs)
	log.Printf("Blueprint: cleaned user list (%d)\n", len(hs.Users))

	bp.Homeservers = append(bp.Homeservers, hs)

	return bp, nil
}

func convertRoom(sr *AnonSnapshotRoom) *b.Room {
	if len(sr.State) == 0 {
		return convertTimelineOnlyRoom(sr)
	}
	r := &b.Room{
		Ref:     sr.ID,
		Creator: sr.Creator,
	}
	// find and set the create content and memberships
	memberships := map[string][]string{}
	otherState := []json.RawMessage{}
	var creatorMembership string
	var plEvent json.RawMessage
	for _, ev := range sr.State {
		evType := gjson.GetBytes(ev, "type").Str
		if ignoredEventType[evType] {
			continue
		}
		switch evType {
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
			if userID == sr.Creator {
				// we handle the creator separately as they are the magic user who sets room pre-state
				creatorMembership = membership
			} else {
				memberships[membership] = append(memberships[membership], userID)
			}
		case "m.room.power_levels":
			plEvent = ev
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
	/*
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
		} */
	// All invited users should be invited, we'll just invite them from the creator as they will be joined with perms
	for _, userID := range memberships["invite"] {
		r.Events = append(r.Events, b.Event{
			Sender:   sr.Creator,
			StateKey: b.Ptr(userID),
			Type:     "m.room.member",
			Content: map[string]interface{}{
				"membership": "invite",
			},
		})
	}
	// All banned users should be banned, we'll ban them from the creator as they will be joined with perms
	for _, userID := range memberships["ban"] {
		r.Events = append(r.Events, b.Event{
			Sender:   sr.Creator,
			StateKey: b.Ptr(userID),
			Type:     "m.room.member",
			Content: map[string]interface{}{
				"membership": "ban",
			},
		})
	}
	// Set all the /sync State (excluding create/member events) from the creator as they will be joined with perms
	// the PL event comes last as it may make the creator unable to do stuff
	for _, ev := range otherState {
		r.Events = append(r.Events, b.Event{
			Sender:   sr.Creator,
			StateKey: b.Ptr(gjson.GetBytes(ev, "state_key").Str),
			Type:     gjson.GetBytes(ev, "type").Str,
			Content:  jsonObject([]byte(gjson.GetBytes(ev, "content").Raw)),
		})
	}
	if plEvent != nil {
		r.Events = append(r.Events, b.Event{
			Sender:   sr.Creator,
			StateKey: b.Ptr(""),
			Type:     "m.room.power_levels",
			Content:  jsonObject([]byte(gjson.GetBytes(plEvent, "content").Raw)),
		})
	}

	// roll forward Timeline
	for _, ev := range sr.Timeline {
		evType := gjson.GetBytes(ev, "type").Str
		if ignoredEventType[evType] {
			continue
		}
		var sk *string
		skg := gjson.GetBytes(ev, "state_key")
		if skg.Exists() {
			sk = &skg.Str
		}
		if evType == "m.room.member" && sk != nil {
			membership := gjson.GetBytes(ev, "content.membership").Str
			if *sk == sr.Creator {
				// merge multiple creator memberships into one so we never end up leaving the room completely empty
				// which can happen if everyone leaves the room in State but then joins again in Timeline
				creatorMembership = membership
				continue
			} else if membership == "leave" {
				// we can't do leave -> leave transitions else we get "User \"@anon-4c9:hs1\" is not a member of room"
				// so they must either be invite/join/ban
				canBeLeft := false
				for _, uid := range memberships["invite"] {
					if uid == *sk {
						canBeLeft = true
						break
					}
				}
				for _, uid := range memberships["join"] {
					if uid == *sk {
						canBeLeft = true
						break
					}
				}
				for _, uid := range memberships["ban"] {
					if uid == *sk {
						canBeLeft = true
						break
					}
				}
				if !canBeLeft {
					continue
				}
			}
		}
		r.Events = append(r.Events, b.Event{
			Sender:   gjson.GetBytes(ev, "sender").Str,
			StateKey: sk,
			Type:     evType,
			Content:  jsonObject([]byte(gjson.GetBytes(ev, "content").Raw)),
		})
	}

	// NOW handle the creator's membership
	// We need to inspect the PL event to accurately handle invite/ban
	switch creatorMembership {
	case "invite": // TODO: handle this properly
		fallthrough
	case "ban": // TODO: handle this properly
		fallthrough
	case "leave":
		r.Events = append(r.Events, b.Event{
			Sender:   sr.Creator,
			StateKey: b.Ptr(sr.Creator),
			Type:     "m.room.member",
			Content: map[string]interface{}{
				"membership": "leave",
			},
		})
	}

	return r
}

func convertTimelineOnlyRoom(sr *AnonSnapshotRoom) *b.Room {
	r := &b.Room{
		Ref:     sr.ID,
		Creator: sr.Creator,
	}
	for _, ev := range sr.Timeline {
		evType := gjson.GetBytes(ev, "type").Str
		if ignoredEventType[evType] {
			continue
		}
		switch evType {
		case "m.room.create":
			var createContent map[string]interface{}
			if err := json.Unmarshal([]byte(gjson.GetBytes(ev, "content").Raw), &createContent); err != nil {
				log.Printf("  cannot convert room, cannot unmarshal m.room.create content: %s\n", err)
				return nil
			}
			r.CreateRoom = createContent
		default:
			var sk *string
			skg := gjson.GetBytes(ev, "state_key")
			if skg.Exists() {
				sk = &skg.Str
			}
			r.Events = append(r.Events, b.Event{
				Sender:   gjson.GetBytes(ev, "sender").Str,
				StateKey: sk,
				Type:     evType,
				Content:  jsonObject([]byte(gjson.GetBytes(ev, "content").Raw)),
			})
		}
	}
	return r
}

func removeUnusedUsers(hs *b.Homeserver) {
	usersWhoDoSomething := make(map[string]bool)
	for _, room := range hs.Rooms {
		for _, ev := range room.Events {
			if ev.Type == "m.room.member" && ev.StateKey != nil {
				localpartStateKey, _ := split(*ev.StateKey)
				usersWhoDoSomething[localpartStateKey] = true
			}
			localpart, _ := split(ev.Sender)
			usersWhoDoSomething[localpart] = true
		}
	}
	var users []b.User
	for i := 0; i < len(hs.Users); i++ {
		if !usersWhoDoSomething[hs.Users[i].Localpart] {
			continue
		}
		users = append(users, hs.Users[i])
	}
	hs.Users = users
}

func jsonObject(in json.RawMessage) (out map[string]interface{}) {
	_ = json.Unmarshal(in, &out)
	return
}
