package internal

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// NoEncryptedDevice is the device ID used when there are no E2E messages sent by this user
const NoEncryptedDevice = "device-default"

// Snapshot is the output produced by this script
type Snapshot struct {
	Rooms          []AnonSnapshotRoom
	Servers        []string
	AccountDataDMs map[string][]string
	Devices        map[string][]string
	UserID         string // the anonymous user whose snapshot this is - important for setting account data
}

// @localpart:domain
var userIDRegexp = regexp.MustCompile(`@[A-Za-z0-9\-\.=_/]+:[A-Za-z0-9\-\.=_/]+`)

// Redact syncData into an anonymised snapshot
func Redact(syncData []byte, anonMappings AnonMappings) *Snapshot {
	sshot := &Snapshot{}
	joins := gjson.GetBytes(syncData, "rooms.join")
	// sort the room IDs
	var joinedRooms []string
	roomIDToRoomData := make(map[string]gjson.Result)
	joins.ForEach(func(k, v gjson.Result) bool {
		joinedRooms = append(joinedRooms, k.Str)
		roomIDToRoomData[k.Str] = v
		return true
	})
	sort.Strings(joinedRooms)
	for i, roomID := range joinedRooms {
		log.Printf("Processing room %s %d/%d\n", roomID, i+1, len(joinedRooms))
		roomData := roomIDToRoomData[roomID]
		room, err := mapAnonRoom(i, roomID, roomData, &anonMappings)
		if err != nil {
			log.Printf("WARNING: skipping room - failed to anonymise room: %s\n", err)
			continue
		}
		sshot.Rooms = append(sshot.Rooms, *room)
		if err != nil {
			log.Printf("WARNING: skipping room - failed to marshal room as JSON: %s\n", err)
			continue
		}
	}
	var anonServers []string
	for _, v := range anonMappings.Servers {
		anonServers = append(anonServers, v)
	}
	var anonUsers []string
	for _, v := range anonMappings.Users {
		anonUsers = append(anonUsers, v)
	}
	sshot.Servers = anonServers
	sshot.AccountDataDMs = processAccountDataDMs(syncData, anonMappings)
	sshot.Devices = anonMappings.deviceMap()
	for _, userID := range anonUsers {
		if _, ok := sshot.Devices[userID]; ok {
			continue
		}
		sshot.Devices[userID] = []string{NoEncryptedDevice}
	}
	return sshot
}

// RedactRules are the rules to apply.
// Only the event types present in this map will be kept, all other ones are dropped.
// Only keys matched in the redact rules are kept, all other keys are dropped, hence some rules are
// pass-through (e.g. room_version, type)
var RedactRules = map[string][]redaction{
	"all": {
		{
			key: "sender",
			replaceWith: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
				return mappings.User(key.Str)
			},
		},
		{
			key: "room_id",
			replaceWith: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
				return anonRoomID
			},
		},
		{
			key: "state_key",
			replaceWith: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
				return mappings.User(key.Str)
			},
			onCondition: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) bool {
				return strings.HasPrefix(key.Str, "@")
			},
		},
		{
			key:         "type",
			replaceWith: passThrough,
		},
	},
	// TODO:
	// m.room.third_party_invite
	//
	"m.room.create": {
		{
			key: "content.creator",
			replaceWith: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
				return mappings.User(key.Str)
			},
		},
		{
			key: "content.predecessor.room_id",
			replaceWith: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
				return mappings.Room(key.Str)
			},
		},
		{
			key:         "content.m\\.federate",
			replaceWith: passThrough,
		},
		{
			key:         "content.room_version",
			replaceWith: passThrough,
		},
	},
	"m.room.name": {
		{
			key: "content.name",
			replaceWith: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
				return strings.Map(redactFunc, key.Str)
			},
		},
	},
	"m.room.topic": {
		{
			key: "content.topic",
			replaceWith: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
				return strings.Map(redactFunc, key.Str)
			},
		},
	},
	"m.room.avatar": {
		{
			key: "content.url",
			replaceWith: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
				return "yes"
			},
		},
		{
			key:         "content.info.h",
			replaceWith: passThrough,
		},
		{
			key:         "content.info.w",
			replaceWith: passThrough,
		},
		{
			key:         "content.info.mimetype",
			replaceWith: passThrough,
		},
		{
			key:         "content.info.size",
			replaceWith: passThrough,
		},
	},
	"m.room.canonical_alias": {
		{
			key: "content.alias",
			replaceWith: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
				_, domain := split(key.Str)
				anonServer := mappings.Server(domain)
				return "#" + strings.Replace(anonRoomID[1:], ":", "", -1) + ":" + anonServer
			},
		},
		// TODO alt_aliases
	},
	"m.room.server_acl": {
		{
			key:         "content.deny",
			replaceWith: redactStringArray,
		},
		{
			key:         "content.allow",
			replaceWith: redactStringArray,
		},
		{
			key:         "content.allow_ip_literals",
			replaceWith: passThrough,
		},
	},
	"m.reaction": {
		{
			key: "content.m\\.relates_to.event_id",
			replaceWith: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
				// TODO: event_id mapper
				return ""
			},
		},
	}, // TODO
	"m.room.encryption": {
		{
			key:         "content.algorithm",
			replaceWith: passThrough,
		},
		{
			key:         "content.rotation_period_ms",
			replaceWith: passThrough,
		},
		{
			key:         "content.rotation_period_msgs",
			replaceWith: passThrough,
		},
	},
	"m.room.guest_access": {
		{
			key:         "content.guest_access",
			replaceWith: passThrough,
		},
	},
	"m.room.history_visibility": {
		{
			key:         "content.history_visibility",
			replaceWith: passThrough,
		},
	},
	"m.room.join_rules": {
		{
			key:         "content.join_rule",
			replaceWith: passThrough,
		},
	},
	"org.matrix.room.preview_urls": {
		{
			key:         "content.disable",
			replaceWith: passThrough,
		},
	},
	"m.room.tombstone": {
		{
			key: "content.body",
			replaceWith: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
				return strings.Map(redactFunc, key.Str)
			},
		},
		{
			key: "content.replacement_room",
			replaceWith: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
				return mappings.Room(key.Str)
			},
		},
	},
	"m.room.pinned_events": {
		{
			key:         "content.pinned",
			replaceWith: redactStringArray,
		},
	},
	"m.room.member": {
		{
			key: "content.avatar_url",
			replaceWith: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
				return "yes"
			},
			onCondition: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) bool {
				return event.Get("type").Str == "m.room.member"
			},
		},
		{
			key: "content.displayname",
			replaceWith: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
				target := mappings.User(event.Get("state_key").Str)
				local, _ := split(target)
				return local
			},
			onCondition: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) bool {
				return event.Get("type").Str == "m.room.member"
			},
		},
		{
			key: "content.reason",
			replaceWith: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
				return strings.Map(redactFunc, key.Str)
			},
		},
		{
			key: "content.inviter",
			replaceWith: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
				return mappings.User(key.Str)
			},
		},
		{
			key:         "content.membership",
			replaceWith: passThrough,
		},
	},
	"m.room.power_levels": {
		{
			key: "content.users",
			replaceWith: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
				// user_id => PL
				result := make(map[string]int)
				key.ForEach(func(k, v gjson.Result) bool {
					result[mappings.User(k.Str)] = int(v.Int())
					return true
				})
				return result
			},
			onCondition: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) bool {
				return event.Get("type").Str == "m.room.power_levels" && event.Get("state_key").Str == ""
			},
		},
		{
			key:         "content.ban",
			replaceWith: passThrough,
		},
		{
			key:         "content.events",
			replaceWith: passThrough,
		},
		{
			key:         "content.events_default",
			replaceWith: passThrough,
		},
		{
			key:         "content.invite",
			replaceWith: passThrough,
		},
		{
			key:         "content.kick",
			replaceWith: passThrough,
		},
		{
			key:         "content.redact",
			replaceWith: passThrough,
		},
		{
			key:         "content.state_default",
			replaceWith: passThrough,
		},
		{
			key:         "content.users_default",
			replaceWith: passThrough,
		},
		{
			key:         "content.notifications",
			replaceWith: passThrough,
		},
	},
	"m.room.encrypted": {
		{
			key: "content",
			replaceWith: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
				// map device ID so we know which device sent the encrypted message.
				deviceID := key.Get("device_id").Str
				return map[string]interface{}{
					"device_id":         mappings.Device(event.Get("sender").Str, deviceID),
					"algorithm":         key.Get("algorithm").Str,
					"ciphertext_length": len(key.Get("ciphertext").Str),
				}
			},
		},
	},
	"m.room.redaction": {
		{
			key: "content",
			replaceWith: func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
				if key.Get("ciphertext").Exists() { // E2E redaction
					// map device ID so we know which device sent the encrypted message.
					deviceID := key.Get("device_id").Str
					return map[string]interface{}{
						"device_id":         mappings.Device(event.Get("sender").Str, deviceID),
						"algorithm":         key.Get("algorithm").Str,
						"ciphertext_length": len(key.Get("ciphertext").Str),
					}
				}
				// normal redaction
				return map[string]interface{}{}
			},
		},
		{
			key:         "content.reason",
			replaceWith: bodyReplacer,
		},
	},
	"m.room.message": {
		{
			key:         "content.msgtype",
			replaceWith: passThrough,
		},
		{
			key:         "content.format",
			replaceWith: passThrough,
		},
		{
			key:         "content.body",
			replaceWith: bodyReplacer,
		},
		{
			key:         "content.m\\.new_content.body",
			replaceWith: bodyReplacer,
		},
	},
}

// passThrough the value as-is without modification
func passThrough(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
	return key.Value()
}

func redact(rawJSON, roomID string, mappings *AnonMappings, redactions []redaction) string {
	val := gjson.Parse(rawJSON)
	// whitelist fields, only fields caught in the redaction rules will be passed through,
	// the rest are dropped.
	outputVal := json.RawMessage(`{"content":{}}`)
	for _, r := range redactions {
		field := val.Get(r.key)
		if !field.Exists() {
			continue
		}
		if r.onCondition != nil {
			if !r.onCondition(mappings, val, field, roomID) {
				continue
			}
		}
		newVal := r.replaceWith(mappings, val, field, roomID)
		if newVal != nil {
			outputVal, _ = sjson.SetBytes(outputVal, r.key, newVal)
		}
	}
	return string(outputVal)
}

func redactStringArray(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
	arrint := key.Value()
	arr, ok := arrint.([]interface{})
	if !ok {
		return []string{}
	}
	var list []string
	for _, a := range arr {
		astr, ok := a.(string)
		if !ok {
			continue
		}
		list = append(list, strings.Map(redactFunc, astr))
	}
	return list
}

func redactFunc(r rune) rune {
	if unicode.IsSpace(r) || unicode.IsPunct(r) {
		return r
	}
	return 'x'
}

func bodyReplacer(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{} {
	// replace any user ID with an anonymous one
	var anonUserIDs []string
	body := userIDRegexp.ReplaceAllStringFunc(key.Str, func(in string) string {
		anon := mappings.User(in)
		anonUserIDs = append(anonUserIDs, anon)
		return anon
	})
	// blank out the entire body
	redactedBody := strings.Map(redactFunc, body)
	// replace @xxx:xxx.xxx with a user ID from anonUserID
	for _, au := range anonUserIDs {
		xu := strings.Map(redactFunc, au)
		redactedBody = strings.Replace(redactedBody, xu, " "+au+" ", 1)
	}

	return redactedBody
}

// AnonMappings contains the mappings to convert live sync data to anonymous sync data. This is done via incremental
// counts rather than hashing which is potentially vulnerable to reverse lookup attacks.
type AnonMappings struct {
	Users             map[string]string          // real user ID -> anonymised
	UsersCount        int                        // counter for generating anonymous user IDs
	Devices           map[string]string          // real device ID -> anonymised device ID
	DevicesCount      int                        // counter for generating anonymous devices
	Servers           map[string]string          // real server name -> anonymised server name
	ServersCount      int                        // counter for generating anonymous servers
	Rooms             map[string]string          // real room ID -> anonymised room ID, counter is from sorted room IDs
	AnonUserToDevices map[string]map[string]bool // anon user -> anon devices
	SingleServerName  string                     // if set, all users get to live on this single server
}

func (a *AnonMappings) Device(userID, deviceID string) string {
	anonDevice, ok := a.Devices[deviceID]
	if ok {
		return anonDevice
	}
	// make an anon device
	anonDevice = fmt.Sprintf("device-%x", a.DevicesCount)
	a.Devices[deviceID] = anonDevice
	a.DevicesCount++
	// store the fact that this user has a device
	anonUser := a.User(userID)
	deviceSet, ok := a.AnonUserToDevices[anonUser]
	if !ok {
		deviceSet = make(map[string]bool)
	}
	deviceSet[anonDevice] = true
	a.AnonUserToDevices[anonUser] = deviceSet

	return anonDevice
}

func (a *AnonMappings) User(userID string) string {
	if len(userID) == 0 || userID[0] != '@' {
		return ""
	}
	anonUser, ok := a.Users[userID]
	if ok {
		return anonUser
	}
	// make an anon user
	_, domain := split(userID)
	if domain == "" {
		return "" // invalid user ID
	}
	anonUser = fmt.Sprintf("@anon-%x:%s", a.UsersCount, a.Server(domain))
	a.Users[userID] = anonUser
	a.UsersCount++
	return anonUser
}

func (a *AnonMappings) Server(realServer string) string {
	if a.SingleServerName != "" {
		return a.SingleServerName
	}
	anonServer, ok := a.Servers[realServer]
	if ok {
		return anonServer
	}
	// make an anon server
	anonServer = fmt.Sprintf("server-%x", a.ServersCount)
	a.Servers[realServer] = anonServer
	a.ServersCount++
	return anonServer
}

func (a *AnonMappings) Room(roomID string) string {
	return a.Rooms[roomID]
}

func (a *AnonMappings) SetRoom(roomID, anonRoomID string) {
	a.Rooms[roomID] = anonRoomID
}

// returns user_id => device_ids
func (a *AnonMappings) deviceMap() map[string][]string {
	result := make(map[string][]string)
	for userID, set := range a.AnonUserToDevices {
		for deviceID := range set {
			result[userID] = append(result[userID], deviceID)
		}
	}
	return result
}

type AnonSnapshotRoom struct {
	ID       string
	Creator  string
	State    []json.RawMessage
	Timeline []json.RawMessage
}

// redaction represents a rule to replace parts of a JSON object
type redaction struct {
	// The key to inspect
	key string // e.g content.displayname
	// Optional function called with the value of the key, return true to replace else false
	onCondition func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) bool
	// The value to replace with
	replaceWith func(mappings *AnonMappings, event, key gjson.Result, anonRoomID string) interface{}
}

// loadSyncData loads the entire sync response either from disk or from a remote HTTP call.

func split(matrixID string) (local, domain string) {
	parts := strings.SplitN(matrixID, ":", 2)
	if len(parts) != 2 {
		log.Printf("Invalid matrix identifier: %s\n", matrixID)
		return
	}
	return parts[0][1:], parts[1]
}

// gjsonEscape escapes . and * from the input so it can be used with gjson.Get
func gjsonEscape(in string) string {
	in = strings.ReplaceAll(in, ".", `\.`)
	in = strings.ReplaceAll(in, "*", `\*`)
	return in
}

// Find the event `type` and `state_key` in one or more gjson arrays, returns first match given.
func findEventInArray(evType, stateKey string, arrs ...gjson.Result) (event *gjson.Result) {
	for _, arr := range arrs {
		arr.ForEach(func(_, v gjson.Result) bool {
			vType := v.Get("type")
			vSk := v.Get("state_key")
			isWantedEvent := vType.Str == evType && vSk.Str == stateKey
			if isWantedEvent {
				event = &v
			}
			return !isWantedEvent
		})
		if event != nil {
			return
		}
	}
	return
}

func mapAnonRoom(index int, roomID string, roomData gjson.Result, mappings *AnonMappings) (*AnonSnapshotRoom, error) {
	// pull out the create event
	stateEvents := roomData.Get("state.events")
	timelineEvents := roomData.Get("timeline.events")
	createEvent := findEventInArray("m.room.create", "", stateEvents, timelineEvents)
	if createEvent == nil {
		return nil, fmt.Errorf("failed to find m.room.create event")
	}
	creator := mappings.User(createEvent.Get("sender").Str)
	if creator == "" {
		return nil, fmt.Errorf("failed to find room creator, create event missing sender")
	}
	_, domain := split(creator)
	anonRoomID := fmt.Sprintf("!%d:%s", index, mappings.Server(domain))
	room := &AnonSnapshotRoom{
		Creator: creator,
		ID:      anonRoomID,
	}
	mappings.SetRoom(roomID, anonRoomID)

	var dropEventType = func(evType string) bool {
		_, ok := RedactRules[evType]
		if ok {
			return false
		}
		log.Println("  dropping event type " + evType)
		return true
	}

	// apply redaction rules "all" and $event_type
	stateEvents.ForEach(func(_, v gjson.Result) bool {
		eventType := v.Get("type").Str
		if dropEventType(eventType) {
			return true
		}
		redactions := RedactRules["all"]
		redactions = append(redactions, RedactRules[eventType]...)
		evJSON := redact(v.Raw, anonRoomID, mappings, redactions)
		// blueprints only care about a few fields, so just add those fields on a whitelist basis
		blueprintEvent := map[string]interface{}{}
		blueprintEvent["sender"] = gjson.Get(evJSON, "sender").Str
		blueprintEvent["type"] = gjson.Get(evJSON, "type").Str
		blueprintEvent["state_key"] = gjson.Get(evJSON, "state_key").Str
		blueprintEvent["content"] = json.RawMessage(gjson.Get(evJSON, "content").Raw)
		be, err := json.Marshal(blueprintEvent)
		if err != nil {
			log.Printf("failed to anonymise event: %s\n", err)
			return true
		}
		room.State = append(room.State, be)
		return true
	})
	timelineEvents.ForEach(func(_, v gjson.Result) bool {
		eventType := v.Get("type").Str
		if dropEventType(eventType) {
			return true
		}
		redactions := RedactRules["all"]
		redactions = append(redactions, RedactRules[eventType]...)
		evJSON := redact(v.Raw, anonRoomID, mappings, redactions)
		// blueprints only care about a few fields, so just add those fields on a whitelist basis
		blueprintEvent := map[string]interface{}{}
		blueprintEvent["sender"] = gjson.Get(evJSON, "sender").Str
		blueprintEvent["type"] = gjson.Get(evJSON, "type").Str
		sk := gjson.Get(evJSON, "state_key")
		if sk.Exists() {
			blueprintEvent["state_key"] = sk.Str
		}
		blueprintEvent["content"] = json.RawMessage(gjson.Get(evJSON, "content").Raw)
		be, err := json.Marshal(blueprintEvent)
		if err != nil {
			log.Printf("failed to anonymise event: %s\n", err)
			return true
		}
		room.Timeline = append(room.Timeline, be)
		return true
	})

	return room, nil
}

func processAccountDataDMs(syncData []byte, anonMappings AnonMappings) (anonDMMap map[string][]string) {
	anonDMMap = make(map[string][]string)
	accData := gjson.GetBytes(syncData, "account_data.events")
	accData.ForEach(func(k, v gjson.Result) bool {
		switch v.Get("type").Str {
		case "m.direct":
			// content: { $user_id : [ $room_id ]}
			dmMap := map[string][]string{}
			err := json.Unmarshal([]byte(v.Get("content").Raw), &dmMap)
			if err != nil {
				log.Printf("Failed to load DM map from account data: %s\n", err)
				return true
			}
			for userID, roomIDs := range dmMap {
				anonRoomIDs := make([]string, len(roomIDs))
				for i, r := range roomIDs {
					anonRoomIDs[i] = anonMappings.Room(r)
				}
				anonDMMap[anonMappings.User(userID)] = anonRoomIDs
			}
		}
		// TODO: push rules
		return true
	})
	return anonDMMap
}
