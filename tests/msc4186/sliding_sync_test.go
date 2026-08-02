package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/tidwall/gjson"
)

const maxSafeJSONInteger = 1<<53 - 1

type slidingSyncEndpoint struct {
	name   string
	paths  []string
	legacy bool
}

var (
	slidingSyncEndpointCandidates = []slidingSyncEndpoint{
		{
			name:  "v4",
			paths: []string{"_matrix", "client", "v4", "sync"},
		},
		{
			name:  "v5",
			paths: []string{"_matrix", "client", "v5", "sync"},
		},
		{
			name:   "unstable-org.matrix.simplified_msc3575",
			paths:  []string{"_matrix", "client", "unstable", "org.matrix.simplified_msc3575", "sync"},
			legacy: true,
		},
	}
	slidingSyncEndpointCacheMu sync.Mutex
	slidingSyncEndpointCache   = map[string]slidingSyncEndpoint{}
)

type slidingSyncReq struct {
	ConnID            string
	Pos               string
	Timeout           int
	SetPresence       string
	Lists             map[string]interface{}
	RoomSubscriptions map[string]interface{}
	Extensions        map[string]interface{}
}

func mustDoSlidingSync(t *testing.T, user *client.CSAPI, req slidingSyncReq) (string, gjson.Result) {
	t.Helper()

	endpoints := slidingSyncEndpointCandidates
	slidingSyncEndpointCacheMu.Lock()
	if cachedEndpoint, ok := slidingSyncEndpointCache[user.BaseURL]; ok {
		endpoints = []slidingSyncEndpoint{cachedEndpoint}
	}
	slidingSyncEndpointCacheMu.Unlock()

	var failures []string
	for _, endpoint := range endpoints {
		pos, body, ok, failure := tryDoSlidingSync(t, user, req, endpoint)
		if ok {
			slidingSyncEndpointCacheMu.Lock()
			slidingSyncEndpointCache[user.BaseURL] = endpoint
			slidingSyncEndpointCacheMu.Unlock()
			return pos, body
		}
		failures = append(failures, failure)
	}

	t.Fatalf("no supported sliding sync endpoint found for %s: %v", user.BaseURL, failures)
	panic("unreachable")
}

// lookupSlidingSyncEndpoint returns the endpoint previously discovered for user by
// mustDoSlidingSync. Callers that need to observe non-2xx responses (e.g. negative
// tests) should establish the endpoint with a successful mustDoSlidingSync call first.
func lookupSlidingSyncEndpoint(t *testing.T, user *client.CSAPI) slidingSyncEndpoint {
	t.Helper()
	endpoint, ok := cachedSlidingSyncEndpoint(user)
	if !ok {
		t.Fatalf("no cached sliding sync endpoint for %s; call mustDoSlidingSync first", user.BaseURL)
	}
	return endpoint
}

func cachedSlidingSyncEndpoint(user *client.CSAPI) (slidingSyncEndpoint, bool) {
	slidingSyncEndpointCacheMu.Lock()
	defer slidingSyncEndpointCacheMu.Unlock()
	endpoint, ok := slidingSyncEndpointCache[user.BaseURL]
	return endpoint, ok
}

func skipIfLegacySlidingSync(t *testing.T, user *client.CSAPI, reason string) {
	t.Helper()
	endpoint, ok := cachedSlidingSyncEndpoint(user)
	if !ok {
		mustDoSlidingSync(t, user, slidingSyncReq{
			ConnID: "legacy-probe-" + strings.NewReplacer("/", "-", " ", "-").Replace(t.Name()),
			Lists:  allRoomsList(1, 0, 0),
		})
		endpoint = lookupSlidingSyncEndpoint(t, user)
	}
	if endpoint.legacy {
		t.Skipf("%s is not supported by the legacy %s sliding sync endpoint", reason, endpoint.name)
	}
}

// doSlidingSyncExpectError issues a sliding sync request against the previously
// discovered endpoint for user and returns the raw status code and body, without
// failing the test on a non-2xx response.
func doSlidingSyncExpectError(t *testing.T, user *client.CSAPI, req slidingSyncReq) (int, gjson.Result) {
	t.Helper()
	endpoint := lookupSlidingSyncEndpoint(t, user)
	return doSlidingSyncRequest(t, user, req, endpoint)
}

func tryDoSlidingSync(t *testing.T, user *client.CSAPI, req slidingSyncReq, endpoint slidingSyncEndpoint) (string, gjson.Result, bool, string) {
	t.Helper()

	statusCode, jsonBody := doSlidingSyncRequest(t, user, req, endpoint)

	if statusCode == http.StatusNotFound || jsonBody.Get("errcode").Str == "M_UNRECOGNIZED" {
		return "", gjson.Result{}, false, fmt.Sprintf("%s returned %d: %s", endpoint.name, statusCode, jsonBody.Raw)
	}
	if statusCode < 200 || statusCode >= 300 {
		t.Fatalf("sliding sync endpoint %s returned non-2xx code: %d - body: %s", endpoint.name, statusCode, jsonBody.Raw)
	}

	pos := jsonBody.Get("pos").Str
	if pos == "" {
		t.Fatalf("sliding sync endpoint %s response missing pos: %s", endpoint.name, jsonBody.Raw)
	}
	return pos, jsonBody, true, ""
}

func doSlidingSyncRequest(t *testing.T, user *client.CSAPI, req slidingSyncReq, endpoint slidingSyncEndpoint) (int, gjson.Result) {
	t.Helper()

	requestBody := slidingSyncRequestBody(req)
	requestOpts := []client.RequestOpt{client.WithJSONBody(t, requestBody)}
	if endpoint.legacy {
		requestBody = legacySlidingSyncRequestBody(requestBody)
		query := url.Values{}
		if req.Pos != "" {
			query.Set("pos", req.Pos)
			query.Set("timeout", strconv.Itoa(req.Timeout))
		}
		requestOpts = []client.RequestOpt{client.WithJSONBody(t, requestBody), client.WithQueries(query)}
	}

	resp := user.Do(t, "POST", endpoint.paths, requestOpts...)
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read sliding sync response body from %s: %s", endpoint.name, err)
	}

	jsonBody := gjson.ParseBytes(respBody)
	if endpoint.legacy && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		jsonBody = normalizeLegacySlidingSyncResponse(t, jsonBody)
	}
	return resp.StatusCode, jsonBody
}

func slidingSyncRequestBody(req slidingSyncReq) map[string]interface{} {
	body := map[string]interface{}{}
	if req.ConnID != "" {
		body["conn_id"] = req.ConnID
	}
	if req.Pos != "" {
		body["pos"] = req.Pos
		body["timeout"] = req.Timeout
	}
	if req.SetPresence != "" {
		body["set_presence"] = req.SetPresence
	}
	if req.Lists != nil {
		body["lists"] = req.Lists
	}
	if req.RoomSubscriptions != nil {
		body["room_subscriptions"] = req.RoomSubscriptions
	}
	if req.Extensions != nil {
		body["extensions"] = req.Extensions
	}

	return body
}

func legacySlidingSyncRequestBody(body map[string]interface{}) map[string]interface{} {
	legacyBody := map[string]interface{}{}
	if connID, ok := body["conn_id"]; ok {
		legacyBody["conn_id"] = connID
	}
	if setPresence, ok := body["set_presence"]; ok {
		legacyBody["set_presence"] = setPresence
	}
	if lists, ok := body["lists"].(map[string]interface{}); ok {
		legacyLists := map[string]interface{}{}
		for listKey, listConfig := range lists {
			legacyLists[listKey] = legacySlidingSyncListConfig(listConfig, true)
		}
		legacyBody["lists"] = legacyLists
	}
	if roomSubscriptions, ok := body["room_subscriptions"].(map[string]interface{}); ok {
		legacyRoomSubscriptions := map[string]interface{}{}
		for roomID, roomConfig := range roomSubscriptions {
			legacyRoomSubscriptions[roomID] = legacySlidingSyncListConfig(roomConfig, false)
		}
		legacyBody["room_subscriptions"] = legacyRoomSubscriptions
	}
	if extensions, ok := body["extensions"]; ok {
		legacyBody["extensions"] = extensions
	}
	return legacyBody
}

func legacySlidingSyncListConfig(config interface{}, includeRanges bool) map[string]interface{} {
	configMap, _ := config.(map[string]interface{})
	legacyConfig := map[string]interface{}{}
	if timelineLimit, ok := configMap["timeline_limit"]; ok {
		legacyConfig["timeline_limit"] = timelineLimit
	}
	if requiredState, ok := configMap["required_state"].(map[string]interface{}); ok {
		legacyConfig["required_state"] = legacyRequiredState(requiredState)
	}
	if filters, ok := configMap["filters"]; ok {
		legacyConfig["filters"] = filters
	}
	if includeRanges {
		if rangeValue, ok := configMap["range"]; ok {
			legacyConfig["ranges"] = []interface{}{rangeValue}
		}
	}
	return legacyConfig
}

func legacyRequiredState(requiredState map[string]interface{}) [][]string {
	legacyState := [][]string{}
	if include, ok := requiredState["include"].([]map[string]interface{}); ok {
		for _, state := range include {
			legacyState = append(legacyState, legacyRequiredStateElement(state))
		}
	} else if include, ok := requiredState["include"].([]interface{}); ok {
		for _, rawState := range include {
			state, _ := rawState.(map[string]interface{})
			legacyState = append(legacyState, legacyRequiredStateElement(state))
		}
	}
	if lazyMembers, _ := requiredState["lazy_members"].(bool); lazyMembers {
		legacyState = append(legacyState, []string{"m.room.member", "$LAZY"})
	}
	return legacyState
}

func legacyRequiredStateElement(state map[string]interface{}) []string {
	eventType := "*"
	stateKey := "*"
	if value, ok := state["type"].(string); ok {
		eventType = value
	}
	if value, ok := state["state_key"].(string); ok {
		stateKey = value
	}
	return []string{eventType, stateKey}
}

func normalizeLegacySlidingSyncResponse(t *testing.T, res gjson.Result) gjson.Result {
	t.Helper()

	var normalized map[string]interface{}
	if err := json.Unmarshal([]byte(res.Raw), &normalized); err != nil {
		t.Fatalf("failed to parse legacy sliding sync response: %s", err)
	}

	roomLists := map[string]map[string]struct{}{}
	if lists, ok := normalized["lists"].(map[string]interface{}); ok {
		for listKey, rawList := range lists {
			list, _ := rawList.(map[string]interface{})
			ops, _ := list["ops"].([]interface{})
			for _, rawOp := range ops {
				op, _ := rawOp.(map[string]interface{})
				roomIDs, _ := op["room_ids"].([]interface{})
				for _, rawRoomID := range roomIDs {
					roomID, _ := rawRoomID.(string)
					if roomID == "" {
						continue
					}
					if _, ok := roomLists[roomID]; !ok {
						roomLists[roomID] = map[string]struct{}{}
					}
					roomLists[roomID][listKey] = struct{}{}
				}
			}
		}
	}

	if rooms, ok := normalized["rooms"].(map[string]interface{}); ok {
		for roomID, rawRoom := range rooms {
			room, _ := rawRoom.(map[string]interface{})
			if timeline, ok := room["timeline_events"]; ok {
				room["timeline"] = timeline
				delete(room, "timeline_events")
			}
			if expandedTimeline, ok := room["unstable_expanded_timeline"]; ok {
				room["expanded_timeline"] = expandedTimeline
				delete(room, "unstable_expanded_timeline")
			}
			if inviteState, ok := room["invite_state"]; ok {
				room["stripped_state"] = inviteState
				delete(room, "invite_state")
			}
			if _, ok := room["lists"]; ok {
				// Some unstable endpoints use the legacy URL but already emit the
				// MSC4186 per-room list membership field. Preserve it instead of
				// deriving membership from legacy list ops.
			} else if lists, ok := roomLists[roomID]; ok {
				room["lists"] = sortedMapKeys(lists)
			} else {
				room["lists"] = []string{}
			}
		}
	}

	normalizedBytes, err := json.Marshal(normalized)
	if err != nil {
		t.Fatalf("failed to encode normalized legacy sliding sync response: %s", err)
	}
	return gjson.ParseBytes(normalizedBytes)
}

func sortedMapKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func slidingList(timelineLimit int, from int, to int) map[string]interface{} {
	return slidingListWithRequiredState(timelineLimit, from, to, map[string]interface{}{
		"include": []map[string]interface{}{},
	})
}

func slidingListWithRequiredState(timelineLimit int, from int, to int, requiredState map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"timeline_limit": timelineLimit,
		"required_state": requiredState,
		"range":          []int{from, to},
	}
}

func slidingListWithFilters(timelineLimit int, from int, to int, filters map[string]interface{}) map[string]interface{} {
	list := slidingList(timelineLimit, from, to)
	list["filters"] = filters
	return list
}

func slidingSubscription(timelineLimit int) map[string]interface{} {
	return map[string]interface{}{
		"timeline_limit": timelineLimit,
		"required_state": map[string]interface{}{
			"include": []map[string]interface{}{},
		},
	}
}

func slidingSubscriptionWithRequiredState(timelineLimit int, requiredState map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"timeline_limit": timelineLimit,
		"required_state": requiredState,
	}
}

func allRoomsList(timelineLimit int, from int, to int) map[string]interface{} {
	return map[string]interface{}{
		"all": slidingList(timelineLimit, from, to),
	}
}

func setRoomTag(t *testing.T, user *client.CSAPI, roomID string, tag string) {
	t.Helper()
	res := user.Do(t, "PUT", []string{"_matrix", "client", "v3", "user", user.UserID, "rooms", roomID, "tags", tag},
		client.WithJSONBody(t, map[string]interface{}{}),
	)
	must.MatchResponse(t, res, match.HTTPResponse{StatusCode: http.StatusOK})
}

// heroUserID extracts a user ID from a heroes array entry, which may be either a
// plain string (legacy m.heroes style) or an object containing a user_id field
// (MSC4186 RoomResult.heroes style).
func heroUserID(hero gjson.Result) string {
	if hero.Type == gjson.String {
		return hero.Str
	}
	return hero.Get("user_id").Str
}

func serverNameOf(userID string) string {
	parts := strings.SplitN(userID, ":", 2)
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}

func roomResult(res gjson.Result, roomID string) gjson.Result {
	return res.Get("rooms." + gjson.Escape(roomID))
}

func requireRoom(t *testing.T, res gjson.Result, roomID string) gjson.Result {
	t.Helper()
	room := roomResult(res, roomID)
	if !room.Exists() {
		t.Fatalf("expected room %s in sliding sync response: %s", roomID, res.Raw)
	}
	return room
}

func requireNoRoom(t *testing.T, res gjson.Result, roomID string) {
	t.Helper()
	if roomResult(res, roomID).Exists() {
		t.Fatalf("did not expect room %s in sliding sync response: %s", roomID, res.Raw)
	}
}

func sendMessage(t *testing.T, user *client.CSAPI, roomID string, body string) string {
	t.Helper()
	return user.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    body,
		},
	})
}

func unsafeSendMessage(t *testing.T, user *client.CSAPI, roomID string, body string) string {
	t.Helper()
	return user.Unsafe_SendEventUnsynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    body,
		},
	})
}

// TestMSC4186SlidingSyncLists verifies initial and incremental sliding sync list responses.
func TestMSC4186SlidingSyncLists(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	t.Run("Initial list returns count and room metadata", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{})
		sendMessage(t, alice, roomID, "hello")

		_, res := mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID: "initial-list",
			Lists:  allRoomsList(1, 0, 0),
		})

		must.MatchGJSON(t, res,
			match.JSONKeyEqual("lists.all.count", float64(1)),
			match.JSONKeyPresent("pos"),
		)

		room := requireRoom(t, res, roomID)
		requireRoomMembership(t, alice, room, "join")
		must.MatchGJSON(t, room,
			match.JSONKeyEqual("initial", true),
			match.JSONKeyArrayOfSize("timeline", 1),
			match.JSONKeyEqual("lists.0", "all"),
		)
		requireBumpStamp(t, room)
	})

	t.Run("Incremental sync only returns rooms with updates", func(t *testing.T) {
		roomA := alice.MustCreateRoom(t, map[string]interface{}{})
		roomB := alice.MustCreateRoom(t, map[string]interface{}{})
		sendMessage(t, alice, roomA, "room A initial")
		sendMessage(t, alice, roomB, "room B initial")

		pos, res := mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID: "incremental-updates",
			Lists:  allRoomsList(1, 0, 9),
		})
		requireRoom(t, res, roomA)
		requireRoom(t, res, roomB)

		eventID := sendMessage(t, alice, roomB, "room B update")

		pos, res = mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID: "incremental-updates",
			Pos:    pos,
			Lists:  allRoomsList(1, 0, 9),
		})

		requireNoRoom(t, res, roomA)
		room := requireRoom(t, res, roomB)
		must.MatchGJSON(t, room,
			match.JSONKeyArrayOfSize("timeline", 1),
			match.JSONKeyEqual("timeline.0.event_id", eventID),
			match.JSONKeyEqual("num_live", float64(1)),
		)
		requireBumpStamp(t, room)

		_, res = mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID: "incremental-updates",
			Pos:    pos,
			Lists:  allRoomsList(1, 0, 9),
		})
		requireNoRoom(t, res, roomA)
		requireNoRoom(t, res, roomB)
	})
}

// TestMSC4186SlidingSyncSubscriptions verifies explicit room subscriptions outside the requested list range.
func TestMSC4186SlidingSyncSubscriptions(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	oldRoom := alice.MustCreateRoom(t, map[string]interface{}{})
	sendMessage(t, alice, oldRoom, "old room")
	newRoom := alice.MustCreateRoom(t, map[string]interface{}{})
	sendMessage(t, alice, newRoom, "new room")

	_, res := mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID: "subscription-outside-range",
		Lists:  allRoomsList(1, 0, 0),
		RoomSubscriptions: map[string]interface{}{
			oldRoom: slidingSubscription(1),
		},
	})

	must.MatchGJSON(t, res,
		match.JSONKeyEqual("lists.all.count", float64(2)),
	)
	requireRoom(t, res, oldRoom)
	requireRoom(t, res, newRoom)
}

// TestMSC4186SlidingSyncListDeltas verifies list membership deltas for subscribed rooms.
//
// NOTE: the current MSC4186 text says the `lists` field is simply omitted once a room
// drops out of every list, which is indistinguishable from the general incremental-sync
// rule that an omitted field means "unchanged" (see msc4186-comments.md, R465). This test
// asserts the fixed behaviour proposed in that comment -- an explicit empty `lists: []`
// -- so it will fail against any server implementing the literal current spec text until
// that wording is amended.
func TestMSC4186SlidingSyncListDeltas(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{})
	sendMessage(t, alice, roomID, "initial top room")

	pos, res := mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID: "list-deltas",
		Lists:  allRoomsList(1, 0, 0),
		RoomSubscriptions: map[string]interface{}{
			roomID: slidingSubscription(1),
		},
	})
	room := requireRoom(t, res, roomID)
	must.MatchGJSON(t, room,
		match.JSONKeyEqual("lists.0", "all"),
	)
	skipIfLegacySlidingSync(t, alice, "incremental list membership deltas for room subscriptions")

	newTopRoom := alice.MustCreateRoom(t, map[string]interface{}{})
	sendMessage(t, alice, newTopRoom, "new top room")

	_, res = mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID: "list-deltas",
		Pos:    pos,
		Lists:  allRoomsList(1, 0, 0),
		RoomSubscriptions: map[string]interface{}{
			roomID: slidingSubscription(1),
		},
	})

	requireRoom(t, res, newTopRoom)
	room = requireRoom(t, res, roomID)
	must.MatchGJSON(t, room,
		match.JSONKeyArrayOfSize("lists", 0),
	)
}

// TestMSC4186SlidingSyncExpandedTimeline verifies timeline expansion on incremental requests.
func TestMSC4186SlidingSyncExpandedTimeline(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{})
	for i := 0; i < 4; i++ {
		sendMessage(t, alice, roomID, fmt.Sprintf("message %d", i))
	}

	pos, res := mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID: "expanded-timeline",
		Lists:  allRoomsList(1, 0, 0),
	})
	room := requireRoom(t, res, roomID)
	must.MatchGJSON(t, room,
		match.JSONKeyArrayOfSize("timeline", 1),
	)

	_, res = mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID: "expanded-timeline",
		Pos:    pos,
		Lists:  allRoomsList(4, 0, 0),
	})
	room = requireRoom(t, res, roomID)
	must.MatchGJSON(t, room,
		match.JSONKeyEqual("expanded_timeline", true),
		match.JSONKeyArrayOfSize("timeline", 4),
	)
}

// TestMSC4186SlidingSyncBulkLoad verifies timeline truncation and bump stamps across many rooms.
func TestMSC4186SlidingSyncBulkLoad(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	messageCounts := []int{15, 20, 25, 30, 35, 40, 45, 50, 60, 75}
	type loadedRoom struct {
		roomID   string
		eventIDs []string
	}
	rooms := make([]loadedRoom, 0, len(messageCounts))

	for roomIndex, messageCount := range messageCounts {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{})
		eventIDs := make([]string, 0, messageCount)
		for messageIndex := 0; messageIndex < messageCount; messageIndex++ {
			eventIDs = append(eventIDs, unsafeSendMessage(
				t, alice, roomID, fmt.Sprintf("room %d message %d", roomIndex, messageIndex),
			))
		}
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, eventIDs[len(eventIDs)-1]))
		rooms = append(rooms, loadedRoom{
			roomID:   roomID,
			eventIDs: eventIDs,
		})
	}

	_, res := mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID: "bulk-load",
		Lists:  allRoomsList(7, 0, len(rooms)-1),
	})
	must.MatchGJSON(t, res,
		match.JSONKeyEqual("lists.all.count", float64(len(rooms))),
	)

	for _, loaded := range rooms {
		room := requireRoom(t, res, loaded.roomID)
		requireBumpStamp(t, room)

		timelineEvents := room.Get("timeline").Array()
		if len(timelineEvents) != 7 {
			t.Fatalf("room %s got %d timeline events, want 7: %s", loaded.roomID, len(timelineEvents), room.Raw)
		}

		wantEventIDs := loaded.eventIDs[len(loaded.eventIDs)-len(timelineEvents):]
		for i, wantEventID := range wantEventIDs {
			gotEventID := timelineEvents[i].Get("event_id").Str
			if gotEventID != wantEventID {
				t.Fatalf("room %s timeline event %d got %s, want %s: %s", loaded.roomID, i, gotEventID, wantEventID, room.Raw)
			}
		}

		if limited := room.Get("limited"); !limited.Exists() || !limited.Bool() {
			t.Fatalf("room %s must set limited=true after truncating %d messages to 7: %s", loaded.roomID, len(loaded.eventIDs), room.Raw)
		}
	}
}

// TestMSC4186SlidingSyncIncrementalRoomDeltas verifies omitted unchanged fields and required state changes.
func TestMSC4186SlidingSyncIncrementalRoomDeltas(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	t.Run("Unchanged room fields are omitted from incremental timeline updates", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{
			"name": "Stable name",
		})

		pos, res := mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID: "incremental-field-deltas",
			Lists:  allRoomsList(1, 0, 0),
		})
		room := requireRoom(t, res, roomID)
		must.MatchGJSON(t, room,
			match.JSONKeyEqual("initial", true),
			match.JSONKeyEqual("name", "Stable name"),
		)

		eventID := sendMessage(t, alice, roomID, "this is the only changed data")

		_, res = mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID: "incremental-field-deltas",
			Pos:    pos,
			Lists:  allRoomsList(1, 0, 0),
		})
		room = requireRoom(t, res, roomID)
		must.MatchGJSON(t, room,
			match.JSONKeyMissing("initial"),
			match.JSONKeyMissing("name"),
			match.JSONKeyArrayOfSize("timeline", 1),
			match.JSONKeyEqual("timeline.0.event_id", eventID),
			match.JSONKeyEqual("num_live", float64(1)),
		)
	})

	t.Run("State changes are delivered as deltas without replaying unchanged state", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{
			"name": "Initial name",
		})
		requiredNameState := map[string]interface{}{
			"include": []map[string]interface{}{
				{"type": "m.room.name", "state_key": ""},
			},
		}

		pos, res := mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID: "incremental-state-deltas",
			Lists: map[string]interface{}{
				"all": slidingListWithRequiredState(1, 0, 0, requiredNameState),
			},
		})
		room := requireRoom(t, res, roomID)
		must.MatchGJSON(t, room,
			match.JSONKeyEqual("initial", true),
			match.JSONKeyEqual("name", "Initial name"),
			match.JSONKeyArrayOfSize("required_state", 1),
			match.JSONKeyEqual("required_state.0.type", "m.room.name"),
			match.JSONKeyEqual("required_state.0.content.name", "Initial name"),
		)

		alice.SendEventSynced(t, roomID, b.Event{
			Type:     "m.room.name",
			StateKey: b.Ptr(""),
			Content:  map[string]interface{}{"name": "Renamed room"},
		})

		_, res = mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID: "incremental-state-deltas",
			Pos:    pos,
			Lists: map[string]interface{}{
				"all": slidingListWithRequiredState(1, 0, 0, requiredNameState),
			},
		})
		room = requireRoom(t, res, roomID)
		must.MatchGJSON(t, room,
			match.JSONKeyMissing("initial"),
			match.JSONKeyEqual("name", "Renamed room"),
			match.JSONKeyArrayOfSize("required_state", 1),
			match.JSONKeyEqual("required_state.0.type", "m.room.name"),
			match.JSONKeyEqual("required_state.0.content.name", "Renamed room"),
		)
	})

	t.Run("Required state expansion returns newly requested state immediately", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{
			"name": "Expanded state room",
		})

		pos, res := mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID: "required-state-expansion",
			Lists:  allRoomsList(1, 0, 0),
		})
		requireRoom(t, res, roomID)

		requiredNameState := map[string]interface{}{
			"include": []map[string]interface{}{
				{"type": "m.room.name", "state_key": ""},
			},
		}
		_, res = mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID: "required-state-expansion",
			Pos:    pos,
			Lists: map[string]interface{}{
				"all": slidingListWithRequiredState(1, 0, 0, requiredNameState),
			},
		})
		room := requireRoom(t, res, roomID)
		must.MatchGJSON(t, room,
			match.JSONKeyMissing("initial"),
			match.JSONKeyArrayOfSize("required_state", 1),
			match.JSONKeyEqual("required_state.0.type", "m.room.name"),
			match.JSONKeyEqual("required_state.0.content.name", "Expanded state room"),
		)
	})
}

// TestMSC4186SlidingSyncMembershipListSemantics verifies leave, kick, and ban visibility in list responses.
func TestMSC4186SlidingSyncMembershipListSemantics(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	t.Run("Voluntary leave before room was sent on connection is omitted", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
		bob.MustJoinRoom(t, roomID, nil)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
		bob.MustLeaveRoom(t, roomID)

		_, res := mustDoSlidingSync(t, bob, slidingSyncReq{
			ConnID: "leave-before-seen",
			Lists:  allRoomsList(1, 0, 9),
		})
		requireNoRoom(t, res, roomID)
	})

	t.Run("Voluntary leave after room was sent on connection is included", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
		bob.MustJoinRoom(t, roomID, nil)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

		pos, res := mustDoSlidingSync(t, bob, slidingSyncReq{
			ConnID: "leave-after-seen",
			Lists:  allRoomsList(1, 0, 9),
		})
		requireRoom(t, res, roomID)

		bob.MustLeaveRoom(t, roomID)

		pos, res = mustDoSlidingSync(t, bob, slidingSyncReq{
			ConnID: "leave-after-seen",
			Pos:    pos,
			Lists:  allRoomsList(1, 0, 9),
		})
		room := requireRoom(t, res, roomID)
		requireRoomMembership(t, bob, room, "leave")
		must.MatchGJSON(t, room,
			match.JSONKeyMissing("initial"),
			match.JSONKeyArrayOfSize("timeline", 1),
			match.JSONKeyEqual("timeline.0.type", "m.room.member"),
			match.JSONKeyEqual("timeline.0.state_key", bob.UserID),
			match.JSONKeyEqual("timeline.0.content.membership", "leave"),
			match.JSONKeyEqual("num_live", float64(1)),
		)
		requireBumpStamp(t, room)

		_, res = mustDoSlidingSync(t, bob, slidingSyncReq{
			ConnID: "leave-after-seen",
			Pos:    pos,
			Lists:  allRoomsList(1, 0, 9),
		})
		requireNoRoom(t, res, roomID)
	})

	t.Run("Kick before room was sent on connection is included", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
		bob.MustJoinRoom(t, roomID, nil)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
		kick(t, alice, roomID, bob.UserID)

		_, res := mustDoSlidingSync(t, bob, slidingSyncReq{
			ConnID: "kick-before-seen",
			Lists:  allRoomsList(1, 0, 9),
		})
		room := requireRoom(t, res, roomID)
		requireRoomMembership(t, bob, room, "leave")
		requireBumpStamp(t, room)
	})

	t.Run("Ban before room was sent on connection is included", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
		bob.MustJoinRoom(t, roomID, nil)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
		ban(t, alice, roomID, bob.UserID)

		_, res := mustDoSlidingSync(t, bob, slidingSyncReq{
			ConnID: "ban-before-seen",
			Lists:  allRoomsList(1, 0, 9),
		})
		room := requireRoom(t, res, roomID)
		requireRoomMembership(t, bob, room, "ban")
		requireBumpStamp(t, room)
	})
}

func requireBumpStamp(t *testing.T, room gjson.Result) {
	t.Helper()
	if !room.Get("bump_stamp").Exists() {
		t.Fatalf("expected bump_stamp in room response: %s", room.Raw)
	}
	requireSafeBumpStamp(t, room)
}

func requireSafeBumpStamp(t *testing.T, room gjson.Result) {
	t.Helper()
	bumpStamp := room.Get("bump_stamp")
	if !bumpStamp.Exists() {
		return
	}
	if bumpStamp.Type != gjson.Number {
		t.Fatalf("bump_stamp must be a number, got %s in %s", bumpStamp.Type, room.Raw)
	}
	value := bumpStamp.Float()
	if math.Trunc(value) != value {
		t.Fatalf("bump_stamp must be an integer, got %v in %s", value, room.Raw)
	}
	if value > maxSafeJSONInteger {
		t.Fatalf("bump_stamp must not exceed 2^53-1, got %.0f in %s", value, room.Raw)
	}
}

func requireRoomMembership(t *testing.T, user *client.CSAPI, room gjson.Result, want string) {
	t.Helper()
	if room.Get("membership").Exists() {
		must.MatchGJSON(t, room, match.JSONKeyEqual("membership", want))
		return
	}

	endpoint := lookupSlidingSyncEndpoint(t, user)
	if !endpoint.legacy {
		t.Fatalf("expected room membership %q in room response: %s", want, room.Raw)
	}

	for _, event := range room.Get("timeline").Array() {
		if event.Get("unsigned.membership").Str == want {
			return
		}
	}
	for _, event := range room.Get("stripped_state").Array() {
		if event.Get("type").Str == "m.room.member" && event.Get("content.membership").Str == want {
			return
		}
	}
	t.Fatalf("expected legacy room membership %q in room response: %s", want, room.Raw)
}

func kick(t *testing.T, user *client.CSAPI, roomID string, targetUserID string) {
	t.Helper()
	res := user.Do(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "kick"},
		client.WithJSONBody(t, map[string]interface{}{
			"user_id": targetUserID,
			"reason":  "testing",
		}),
	)
	must.MatchResponse(t, res, match.HTTPResponse{StatusCode: http.StatusOK})
}

func ban(t *testing.T, user *client.CSAPI, roomID string, targetUserID string) {
	t.Helper()
	res := user.Do(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "ban"},
		client.WithJSONBody(t, map[string]interface{}{
			"user_id": targetUserID,
			"reason":  "testing",
		}),
	)
	must.MatchResponse(t, res, match.HTTPResponse{StatusCode: http.StatusOK})
}

// TestMSC4186SlidingSyncSetPresence verifies the top-level set_presence field updates
// the requesting user's presence state, and that omitting it defaults to online.
func TestMSC4186SlidingSyncSetPresence(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.MustJoinRoom(t, roomID, nil)
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID: "set-presence-probe",
		Lists:  allRoomsList(1, 0, 0),
	})
	skipIfLegacySlidingSync(t, alice, "set_presence presence propagation")

	since := bob.MustSyncUntil(t, client.SyncReq{})

	t.Run("set_presence unavailable updates presence", func(t *testing.T) {
		mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID:      "set-presence-unavailable",
			SetPresence: "unavailable",
			Lists:       allRoomsList(1, 0, 0),
		})
		since = bob.MustSyncUntil(t, client.SyncReq{Since: since}, client.SyncPresenceHas(alice.UserID, b.Ptr("unavailable")))
	})

	t.Run("set_presence offline updates presence", func(t *testing.T) {
		mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID:      "set-presence-offline",
			SetPresence: "offline",
			Lists:       allRoomsList(1, 0, 0),
		})
		since = bob.MustSyncUntil(t, client.SyncReq{Since: since}, client.SyncPresenceHas(alice.UserID, b.Ptr("offline")))
	})

	t.Run("omitted set_presence defaults to online", func(t *testing.T) {
		mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID: "set-presence-default",
			Lists:  allRoomsList(1, 0, 0),
		})
		bob.MustSyncUntil(t, client.SyncReq{Since: since}, client.SyncPresenceHas(alice.UserID, b.Ptr("online")))
	})
}

// TestMSC4186SlidingSyncLongPolling verifies the server blocks on a non-zero timeout
// and unblocks promptly once a new event arrives, rather than waiting out the timeout.
func TestMSC4186SlidingSyncLongPolling(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	roomID := alice.MustCreateRoom(t, map[string]interface{}{})
	sendMessage(t, alice, roomID, "initial")

	pos, _ := mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID: "long-polling",
		Lists:  allRoomsList(1, 0, 0),
	})

	responseChan := make(chan gjson.Result, 1)
	go func() {
		defer close(responseChan)
		_, res := mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID:  "long-polling",
			Pos:     pos,
			Timeout: 20000,
			Lists:   allRoomsList(1, 0, 0),
		})
		responseChan <- res
	}()

	// Give the long poll time to actually start blocking on the server before
	// checking that it hasn't returned prematurely.
	time.Sleep(2 * time.Second)

	select {
	case res := <-responseChan:
		t.Fatalf("sliding sync returned before any new event was sent: %s", res.Raw)
	default:
	}

	eventID := sendMessage(t, alice, roomID, "unblock me")

	select {
	case res := <-responseChan:
		room := requireRoom(t, res, roomID)
		must.MatchGJSON(t, room,
			match.JSONKeyEqual("timeline.0.event_id", eventID),
		)
	case <-time.After(15 * time.Second):
		t.Fatal("sliding sync long poll did not unblock within 15s of a new event")
	}
}

// TestMSC4186SlidingSyncExtensionsToDevice verifies basic parsing and passthrough of
// the top-level extensions node, using the to_device extension as a concrete example.
func TestMSC4186SlidingSyncExtensionsToDevice(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	toDeviceExt := map[string]interface{}{
		"to_device": map[string]interface{}{
			"enabled": true,
		},
	}

	pos, res := mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID:     "extensions-to-device",
		Lists:      allRoomsList(1, 0, 0),
		Extensions: toDeviceExt,
	})
	must.MatchGJSON(t, res,
		match.JSONKeyPresent("extensions.to_device.next_batch"),
	)

	bob.MustSendToDeviceMessages(t, "m.room_key_request", map[string]map[string]map[string]interface{}{
		alice.UserID: {
			"*": {"action": "request", "request_id": "test-request-id"},
		},
	})

	_, res = mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID:     "extensions-to-device",
		Pos:        pos,
		Lists:      allRoomsList(1, 0, 0),
		Extensions: toDeviceExt,
	})

	found := false
	for _, ev := range res.Get("extensions.to_device.events").Array() {
		if ev.Get("sender").Str == bob.UserID && ev.Get("content.request_id").Str == "test-request-id" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected to_device extension to deliver queued message from %s: %s", bob.UserID, res.Raw)
	}
}

// TestMSC4186SlidingSyncListFilters verifies SlidingRoomFilter fields narrow list
// membership as expected.
func TestMSC4186SlidingSyncListFilters(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	t.Run("is_dm filters direct message rooms", func(t *testing.T) {
		dmRoom := alice.MustCreateRoom(t, map[string]interface{}{
			"preset":    "trusted_private_chat",
			"is_direct": true,
			"invite":    []string{bob.UserID},
		})
		alice.MustSetGlobalAccountData(t, "m.direct", map[string]interface{}{
			bob.UserID: []string{dmRoom},
		})
		normalRoom := alice.MustCreateRoom(t, map[string]interface{}{})

		_, res := mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID: "filter-is-dm",
			Lists: map[string]interface{}{
				"dms": slidingListWithFilters(1, 0, 9, map[string]interface{}{"is_dm": true}),
			},
		})
		requireRoom(t, res, dmRoom)
		requireNoRoom(t, res, normalRoom)

		_, res = mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID: "filter-is-dm",
			Lists: map[string]interface{}{
				"dms": slidingListWithFilters(1, 0, 9, map[string]interface{}{"is_dm": false}),
			},
		})
		requireRoom(t, res, normalRoom)
		requireNoRoom(t, res, dmRoom)
	})

	t.Run("is_encrypted filters encrypted rooms", func(t *testing.T) {
		encryptedRoom := alice.MustCreateRoom(t, map[string]interface{}{
			"initial_state": []map[string]interface{}{
				{
					"type":      "m.room.encryption",
					"state_key": "",
					"content":   map[string]interface{}{"algorithm": "m.megolm.v1.aes-sha2"},
				},
			},
		})
		plainRoom := alice.MustCreateRoom(t, map[string]interface{}{})

		_, res := mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID: "filter-is-encrypted",
			Lists: map[string]interface{}{
				"encrypted": slidingListWithFilters(1, 0, 9, map[string]interface{}{"is_encrypted": true}),
			},
		})
		requireRoom(t, res, encryptedRoom)
		requireNoRoom(t, res, plainRoom)
	})

	t.Run("is_invited filters invited-only rooms", func(t *testing.T) {
		invitedRoom := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
		alice.MustInviteRoom(t, invitedRoom, bob.UserID)
		joinedRoom := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
		bob.MustJoinRoom(t, joinedRoom, nil)
		bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, invitedRoom))
		skipIfLegacySlidingSync(t, bob, "is_invited list filtering")

		_, res := mustDoSlidingSync(t, bob, slidingSyncReq{
			ConnID: "filter-is-invited",
			Lists: map[string]interface{}{
				"invited": slidingListWithFilters(1, 0, 9, map[string]interface{}{"is_invited": true}),
			},
		})
		requireRoom(t, res, invitedRoom)
		requireNoRoom(t, res, joinedRoom)
	})

	t.Run("room_types and not_room_types filter spaces", func(t *testing.T) {
		spaceRoom := alice.MustCreateRoom(t, map[string]interface{}{
			"creation_content": map[string]interface{}{"type": "m.space"},
		})
		normalRoom := alice.MustCreateRoom(t, map[string]interface{}{})

		_, res := mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID: "filter-room-types",
			Lists: map[string]interface{}{
				"spaces": slidingListWithFilters(1, 0, 9, map[string]interface{}{"room_types": []interface{}{"m.space"}}),
			},
		})
		requireRoom(t, res, spaceRoom)
		requireNoRoom(t, res, normalRoom)

		_, res = mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID: "filter-not-room-types",
			Lists: map[string]interface{}{
				"non-spaces": slidingListWithFilters(1, 0, 9, map[string]interface{}{"not_room_types": []interface{}{"m.space"}}),
			},
		})
		requireRoom(t, res, normalRoom)
		requireNoRoom(t, res, spaceRoom)
	})

	t.Run("tags and not_tags filter tagged rooms", func(t *testing.T) {
		favouriteRoom := alice.MustCreateRoom(t, map[string]interface{}{})
		setRoomTag(t, alice, favouriteRoom, "m.favourite")
		untaggedRoom := alice.MustCreateRoom(t, map[string]interface{}{})

		_, res := mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID: "filter-tags",
			Lists: map[string]interface{}{
				"favourites": slidingListWithFilters(1, 0, 9, map[string]interface{}{"tags": []interface{}{"m.favourite"}}),
			},
		})
		requireRoom(t, res, favouriteRoom)
		requireNoRoom(t, res, untaggedRoom)

		_, res = mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID: "filter-not-tags",
			Lists: map[string]interface{}{
				"non-favourites": slidingListWithFilters(1, 0, 9, map[string]interface{}{"not_tags": []interface{}{"m.favourite"}}),
			},
		})
		requireRoom(t, res, untaggedRoom)
		requireNoRoom(t, res, favouriteRoom)
	})

	t.Run("spaces filters rooms by space membership", func(t *testing.T) {
		serverName := serverNameOf(alice.UserID)
		spaceRoom := alice.MustCreateRoom(t, map[string]interface{}{
			"creation_content": map[string]interface{}{"type": "m.space"},
		})
		childRoom := alice.MustCreateRoom(t, map[string]interface{}{})
		alice.SendEventSynced(t, spaceRoom, b.Event{
			Type:     "m.space.child",
			StateKey: b.Ptr(childRoom),
			Content: map[string]interface{}{
				"via": []string{serverName},
			},
		})
		unrelatedRoom := alice.MustCreateRoom(t, map[string]interface{}{})
		skipIfLegacySlidingSync(t, alice, "spaces list filtering")

		_, res := mustDoSlidingSync(t, alice, slidingSyncReq{
			ConnID: "filter-spaces",
			Lists: map[string]interface{}{
				"in-space": slidingListWithFilters(1, 0, 9, map[string]interface{}{"spaces": []interface{}{spaceRoom}}),
			},
		})
		requireRoom(t, res, childRoom)
		requireNoRoom(t, res, unrelatedRoom)
		requireNoRoom(t, res, spaceRoom)
	})
}

// TestMSC4186SlidingSyncRangeShifting verifies that growing a list's range across
// requests (e.g. [0,1] then [0,4]) returns the newly-visible rooms as deltas without
// re-sending rooms that were already visible and unchanged.
func TestMSC4186SlidingSyncRangeShifting(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomIDs := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{})
		sendMessage(t, alice, roomID, fmt.Sprintf("room %d initial", i))
		roomIDs = append(roomIDs, roomID)
	}
	// roomIDs[4] is the most recently active room, so with recency sort it should
	// occupy position 0 and roomIDs[0] should occupy position 4.
	top := []string{roomIDs[4], roomIDs[3]}
	newlyVisible := []string{roomIDs[2], roomIDs[1], roomIDs[0]}

	pos, res := mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID: "range-shifting",
		Lists:  allRoomsList(1, 0, 1),
	})
	must.MatchGJSON(t, res, match.JSONKeyEqual("lists.all.count", float64(5)))
	for _, roomID := range top {
		room := requireRoom(t, res, roomID)
		must.MatchGJSON(t, room, match.JSONKeyEqual("initial", true))
	}
	for _, roomID := range newlyVisible {
		requireNoRoom(t, res, roomID)
	}

	_, res = mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID: "range-shifting",
		Pos:    pos,
		Lists:  allRoomsList(1, 0, 4),
	})
	must.MatchGJSON(t, res, match.JSONKeyEqual("lists.all.count", float64(5)))
	for _, roomID := range newlyVisible {
		room := requireRoom(t, res, roomID)
		must.MatchGJSON(t, room, match.JSONKeyEqual("initial", true))
	}
	// Rooms already visible in the prior response should be omitted entirely from
	// this delta, since nothing about them changed.
	for _, roomID := range top {
		requireNoRoom(t, res, roomID)
	}
}

// TestMSC4186SlidingSyncLazyLoading verifies lazy_members:true only returns member
// state for senders relevant to the returned timeline, and expands as new senders
// become relevant.
func TestMSC4186SlidingSyncLazyLoading(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	charlie := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.MustJoinRoom(t, roomID, nil)
	charlie.MustJoinRoom(t, roomID, nil)
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(charlie.UserID, roomID))

	sendMessage(t, alice, roomID, "from bob's perspective")
	sendMessage(t, bob, roomID, "bob speaks")

	lazyRequiredState := map[string]interface{}{
		"include":      []map[string]interface{}{},
		"lazy_members": true,
	}

	pos, res := mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID: "lazy-loading",
		Lists: map[string]interface{}{
			"all": slidingListWithRequiredState(1, 0, 0, lazyRequiredState),
		},
	})
	room := requireRoom(t, res, roomID)
	requiredState := room.Get("required_state").Array()

	var sawBob, sawCharlie bool
	for _, ev := range requiredState {
		if ev.Get("type").Str != "m.room.member" {
			continue
		}
		switch ev.Get("state_key").Str {
		case bob.UserID:
			sawBob = true
		case charlie.UserID:
			sawCharlie = true
		}
	}
	if !sawBob {
		t.Fatalf("expected lazy_members to include bob's member event since he sent the returned timeline event: %s", room.Raw)
	}
	if sawCharlie {
		t.Fatalf("expected lazy_members to omit charlie's member event since he sent no relevant timeline events: %s", room.Raw)
	}

	// Once charlie sends a message, an incremental non-gappy sync should lazily
	// expand to include his member event too.
	sendMessage(t, charlie, roomID, "charlie speaks")

	_, res = mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID: "lazy-loading",
		Pos:    pos,
		Lists: map[string]interface{}{
			"all": slidingListWithRequiredState(1, 0, 0, lazyRequiredState),
		},
	})
	room = requireRoom(t, res, roomID)
	sawCharlie = false
	for _, ev := range room.Get("required_state").Array() {
		if ev.Get("type").Str == "m.room.member" && ev.Get("state_key").Str == charlie.UserID {
			sawCharlie = true
		}
	}
	if !sawCharlie {
		t.Fatalf("expected lazy_members to expand to include charlie's member event after he spoke: %s", room.Raw)
	}
}

// TestMSC4186SlidingSyncRequiredStateExclude verifies required_state.exclude omits
// matching state events even when a broader include would otherwise return them.
func TestMSC4186SlidingSyncRequiredStateExclude(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"name":   "Exclude members room",
	})
	bob.MustJoinRoom(t, roomID, nil)
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
	skipIfLegacySlidingSync(t, alice, "required_state.exclude")

	requiredState := map[string]interface{}{
		"include": []map[string]interface{}{
			{"type": "*", "state_key": "*"},
		},
		"exclude": []map[string]interface{}{
			{"type": "m.room.member", "state_key": "*"},
		},
	}

	_, res := mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID: "required-state-exclude",
		Lists: map[string]interface{}{
			"all": slidingListWithRequiredState(1, 0, 0, requiredState),
		},
	})
	room := requireRoom(t, res, roomID)

	var sawName, sawMember bool
	for _, ev := range room.Get("required_state").Array() {
		switch ev.Get("type").Str {
		case "m.room.name":
			sawName = true
		case "m.room.member":
			sawMember = true
		}
	}
	if !sawName {
		t.Fatalf("expected required_state to include m.room.name via the wildcard include: %s", room.Raw)
	}
	if sawMember {
		t.Fatalf("expected required_state.exclude to omit all m.room.member events: %s", room.Raw)
	}
}

// TestMSC4186SlidingSyncInviteStrippedState verifies that a remote invite is
// delivered with a stripped_state payload describing the room.
func TestMSC4186SlidingSyncInviteStrippedState(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"name":   "Invite room",
		"invite": []string{bob.UserID},
	})
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))

	_, res := mustDoSlidingSync(t, bob, slidingSyncReq{
		ConnID: "invite-stripped-state",
		Lists:  allRoomsList(1, 0, 9),
	})
	room := requireRoom(t, res, roomID)
	requireRoomMembership(t, bob, room, "invite")

	var sawName, sawInvite bool
	for _, ev := range room.Get("stripped_state").Array() {
		if ev.Get("type").Str == "m.room.name" && ev.Get("content.name").Str == "Invite room" {
			sawName = true
		}
		if ev.Get("type").Str == "m.room.member" && ev.Get("state_key").Str == bob.UserID && ev.Get("content.membership").Str == "invite" {
			sawInvite = true
		}
	}
	if !sawName {
		t.Fatalf("expected stripped_state to include m.room.name: %s", room.Raw)
	}
	if !sawInvite {
		t.Fatalf("expected stripped_state to include the invitee's m.room.member event: %s", room.Raw)
	}
}

// TestMSC4186SlidingSyncHeroes verifies that a room without an explicit m.room.name
// returns a heroes payload describing other members.
func TestMSC4186SlidingSyncHeroes(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.MustJoinRoom(t, roomID, nil)
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	_, res := mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID: "heroes",
		Lists:  allRoomsList(1, 0, 9),
	})
	room := requireRoom(t, res, roomID)
	must.MatchGJSON(t, room,
		match.JSONKeyMissing("name"),
	)

	heroes := room.Get("heroes").Array()
	if len(heroes) == 0 {
		t.Fatalf("expected heroes to be populated for a room with no name: %s", room.Raw)
	}
	found := false
	for _, hero := range heroes {
		if heroUserID(hero) == bob.UserID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected heroes to include %s: %s", bob.UserID, room.Raw)
	}
}

// TestMSC4186SlidingSyncRoomMetadata verifies joined_count, invited_count, avatar,
// and prev_batch fields on RoomResult.
func TestMSC4186SlidingSyncRoomMetadata(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	charlie := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"initial_state": []map[string]interface{}{
			{
				"type":      "m.room.avatar",
				"state_key": "",
				"content":   map[string]interface{}{"url": "mxc://example.com/avatar"},
			},
		},
	})
	bob.MustJoinRoom(t, roomID, nil)
	alice.MustInviteRoom(t, roomID, charlie.UserID)
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	for i := 0; i < 3; i++ {
		sendMessage(t, alice, roomID, fmt.Sprintf("msg %d", i))
	}

	_, res := mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID: "room-metadata",
		Lists:  allRoomsList(1, 0, 9),
	})
	room := requireRoom(t, res, roomID)
	must.MatchGJSON(t, room,
		match.JSONKeyEqual("joined_count", float64(2)),
		match.JSONKeyEqual("invited_count", float64(1)),
		match.JSONKeyEqual("avatar", "mxc://example.com/avatar"),
		match.JSONKeyPresent("prev_batch"),
	)
}

// TestMSC4186SlidingSyncUnknownPos verifies that an unrecognised or expired pos
// token is rejected with 400 M_UNKNOWN_POS.
func TestMSC4186SlidingSyncUnknownPos(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	alice.MustCreateRoom(t, map[string]interface{}{})

	mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID: "unknown-pos",
		Lists:  allRoomsList(1, 0, 0),
	})

	statusCode, body := doSlidingSyncExpectError(t, alice, slidingSyncReq{
		ConnID: "unknown-pos",
		Pos:    "this-pos-value-does-not-exist-1234567890",
		Lists:  allRoomsList(1, 0, 0),
	})

	if statusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown pos, got %d: %s", statusCode, body.Raw)
	}
	must.MatchGJSON(t, body, match.JSONKeyEqual("errcode", "M_UNKNOWN_POS"))
}

// TestMSC4186SlidingSyncPosOwnership verifies that a pos issued to one user cannot be
// used by a different user to resume that connection, per the Security considerations
// requirement added in response to FCP review: "Servers MUST reject a pos that was not
// issued to the requesting user and device, responding with M_UNKNOWN_POS."
func TestMSC4186SlidingSyncPosOwnership(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.MustJoinRoom(t, roomID, nil)
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	aliceConnID := "pos-ownership-alice"
	alicePos, _ := mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID: aliceConnID,
		Lists:  allRoomsList(1, 0, 0),
	})

	// Prime bob's own endpoint cache under a distinct conn_id first, so the request
	// below (which deliberately reuses alice's conn_id and pos) can't spuriously
	// succeed by hitting a fresh connection of bob's own.
	mustDoSlidingSync(t, bob, slidingSyncReq{
		ConnID: "pos-ownership-bob-probe",
		Lists:  allRoomsList(1, 0, 0),
	})

	statusCode, body := doSlidingSyncExpectError(t, bob, slidingSyncReq{
		ConnID: aliceConnID,
		Pos:    alicePos,
		Lists:  allRoomsList(1, 0, 0),
	})

	if statusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 when bob reuses alice's conn_id/pos, got %d: %s", statusCode, body.Raw)
	}
	must.MatchGJSON(t, body, match.JSONKeyEqual("errcode", "M_UNKNOWN_POS"))
}

// TestMSC4186SlidingSyncMaxLimits verifies the server enforces the 100 lists / 100
// room subscriptions per-request limit with 400 M_INVALID_PARAM.
func TestMSC4186SlidingSyncMaxLimits(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	existingRoomID := alice.MustCreateRoom(t, map[string]interface{}{})
	serverName := serverNameOf(alice.UserID)

	mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID: "max-limits",
		Lists:  allRoomsList(1, 0, 0),
	})

	t.Run("More than 100 lists is rejected", func(t *testing.T) {
		lists := map[string]interface{}{}
		for i := 0; i < 101; i++ {
			lists[fmt.Sprintf("list-%d", i)] = slidingList(1, 0, 0)
		}
		statusCode, body := doSlidingSyncExpectError(t, alice, slidingSyncReq{
			ConnID: "max-limits-lists",
			Lists:  lists,
		})
		if statusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 for more than 100 lists, got %d: %s", statusCode, body.Raw)
		}
		wantErrcode := "M_INVALID_PARAM"
		if lookupSlidingSyncEndpoint(t, alice).legacy {
			wantErrcode = "M_BAD_JSON"
		}
		must.MatchGJSON(t, body, match.JSONKeyEqual("errcode", wantErrcode))
	})

	t.Run("More than 100 room subscriptions is rejected", func(t *testing.T) {
		skipIfLegacySlidingSync(t, alice, "the 100 room subscription request limit")
		subs := map[string]interface{}{
			existingRoomID: slidingSubscription(1),
		}
		for i := 0; i < 100; i++ {
			subs[fmt.Sprintf("!nonexistent-%d:%s", i, serverName)] = slidingSubscription(1)
		}
		statusCode, body := doSlidingSyncExpectError(t, alice, slidingSyncReq{
			ConnID:            "max-limits-subs",
			Lists:             allRoomsList(1, 0, 0),
			RoomSubscriptions: subs,
		})
		if statusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 for more than 100 room subscriptions, got %d: %s", statusCode, body.Raw)
		}
		must.MatchGJSON(t, body, match.JSONKeyEqual("errcode", "M_INVALID_PARAM"))
	})
}

// TestMSC4186SlidingSyncCombiningRoomConfigs verifies that when a room matches multiple
// lists with different room configs, the server serves the superset: the maximum
// timeline_limit and the union of required_state across all matching configs.
func TestMSC4186SlidingSyncCombiningRoomConfigs(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"name": "Combined config room",
		"initial_state": []map[string]interface{}{
			{
				"type":      "m.room.topic",
				"state_key": "",
				"content":   map[string]interface{}{"topic": "Combined config topic"},
			},
		},
	})
	for i := 0; i < 3; i++ {
		sendMessage(t, alice, roomID, fmt.Sprintf("message %d", i))
	}

	nameOnly := map[string]interface{}{
		"include": []map[string]interface{}{
			{"type": "m.room.name", "state_key": ""},
		},
	}
	topicOnly := map[string]interface{}{
		"include": []map[string]interface{}{
			{"type": "m.room.topic", "state_key": ""},
		},
	}

	_, res := mustDoSlidingSync(t, alice, slidingSyncReq{
		ConnID: "combining-room-configs",
		Lists: map[string]interface{}{
			"narrow-timeline-name-only": slidingListWithRequiredState(1, 0, 0, nameOnly),
			"wide-timeline-topic-only":  slidingListWithRequiredState(3, 0, 0, topicOnly),
		},
	})

	room := requireRoom(t, res, roomID)
	must.MatchGJSON(t, room,
		match.JSONKeyArrayOfSize("timeline", 3),
	)

	var sawName, sawTopic bool
	for _, ev := range room.Get("required_state").Array() {
		switch ev.Get("type").Str {
		case "m.room.name":
			sawName = true
		case "m.room.topic":
			sawTopic = true
		}
	}
	if !sawName {
		t.Fatalf("expected combined required_state to include m.room.name from the narrow-timeline list: %s", room.Raw)
	}
	if !sawTopic {
		t.Fatalf("expected combined required_state to include m.room.topic from the wide-timeline list: %s", room.Raw)
	}
}
