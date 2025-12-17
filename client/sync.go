package client

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/matrix-org/complement/ct"
	"github.com/tidwall/gjson"
)

// SyncCheckOpt is a functional option for use with MustSyncUntil which should return <nil> if
// the response satisfies the check, else return a human friendly error.
// The result object is the entire /sync response from this request.
type SyncCheckOpt func(clientUserID string, topLevelSyncJSON gjson.Result) error

// SyncReq contains all the /sync request configuration options. The empty struct `SyncReq{}` is valid
// which will do a full /sync due to lack of a since token.
type SyncReq struct {
	// A point in time to continue a sync from. This should be the next_batch token returned by an
	// earlier call to this endpoint.
	Since string
	// The ID of a filter created using the filter API or a filter JSON object encoded as a string.
	// The server will detect whether it is an ID or a JSON object by whether the first character is
	// a "{" open brace. Passing the JSON inline is best suited to one off requests. Creating a
	// filter using the filter API is recommended for clients that reuse the same filter multiple
	// times, for example in long poll requests.
	Filter string
	// Controls whether to include the full state for all rooms the user is a member of.
	// If this is set to true, then all state events will be returned, even if since is non-empty.
	// The timeline will still be limited by the since parameter. In this case, the timeout parameter
	// will be ignored and the query will return immediately, possibly with an empty timeline.
	// If false, and since is non-empty, only state which has changed since the point indicated by
	// since will be returned.
	// By default, this is false.
	FullState bool
	// Controls whether the client is automatically marked as online by polling this API. If this
	// parameter is omitted then the client is automatically marked as online when it uses this API.
	// Otherwise if the parameter is set to “offline” then the client is not marked as being online
	// when it uses this API. When set to “unavailable”, the client is marked as being idle.
	// One of: [offline online unavailable].
	SetPresence string
	// The maximum time to wait, in milliseconds, before returning this request. If no events
	// (or other data) become available before this time elapses, the server will return a response
	// with empty fields.
	// By default, this is 1000 for Complement testing.
	TimeoutMillis string // string for easier conversion to query params
}

// MustSyncUntil blocks and continually calls /sync (advancing the since token) until all the
// check functions return no error. Returns the final/latest since token.
//
// Initial /sync example: (no since token)
//
//	bob.InviteRoom(t, roomID, alice.UserID)
//	alice.JoinRoom(t, roomID, nil)
//	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))
//
// Incremental /sync example: (test controls since token)
//
//	since := alice.MustSyncUntil(t, client.SyncReq{TimeoutMillis: "0"}) // get a since token
//	bob.InviteRoom(t, roomID, alice.UserID)
//	since = alice.MustSyncUntil(t, client.SyncReq{Since: since}, client.SyncInvitedTo(alice.UserID, roomID))
//	alice.JoinRoom(t, roomID, nil)
//	alice.MustSyncUntil(t, client.SyncReq{Since: since}, client.SyncJoinedTo(alice.UserID, roomID))
//
// Checking multiple parts of /sync:
//
//	alice.MustSyncUntil(
//	    t, client.SyncReq{},
//	    client.SyncJoinedTo(alice.UserID, roomID),
//	    client.SyncJoinedTo(alice.UserID, roomID2),
//	    client.SyncJoinedTo(alice.UserID, roomID3),
//	)
//
// Check functions are unordered and independent. Once a check function returns true it is removed
// from the list of checks and won't be called again.
//
// In the unlikely event that you want all the checkers to pass *explicitly* in a single /sync
// response (e.g to assert some form of atomic update which updates multiple parts of the /sync
// response at once) then make your own checker function which does this.
//
// In the unlikely event that you need ordering on your checks, call MustSyncUntil multiple times
// with a single checker, and reuse the returned since token, as in the "Incremental sync" example.
//
// Will time out after CSAPI.SyncUntilTimeout. Returns the `next_batch` token from the final
// response.
func (c *CSAPI) MustSyncUntil(t ct.TestLike, syncReq SyncReq, checks ...SyncCheckOpt) string {
	t.Helper()
	start := time.Now()
	numResponsesReturned := 0
	checkers := make([]struct {
		check SyncCheckOpt
		errs  []string
	}, len(checks))
	for i := range checks {
		c := checkers[i]
		c.check = checks[i]
		checkers[i] = c
	}
	printErrors := func() string {
		err := "Checkers:\n"
		for _, c := range checkers {
			err += strings.Join(c.errs, "\n")
			err += ", \n"
		}
		return err
	}
	for {
		if time.Since(start) > c.SyncUntilTimeout {
			ct.Fatalf(t, "%s MustSyncUntil: timed out after %v. Seen %d /sync responses. %s", c.UserID, time.Since(start), numResponsesReturned, printErrors())
		}
		response, nextBatch := c.MustSync(t, syncReq)
		syncReq.Since = nextBatch
		numResponsesReturned += 1

		for i := 0; i < len(checkers); i++ {
			err := checkers[i].check(c.UserID, response)
			if err == nil {
				// check passed, removed from checkers
				checkers = append(checkers[:i], checkers[i+1:]...)
				i--
			} else {
				c := checkers[i]
				c.errs = append(c.errs, fmt.Sprintf("[t=%v] Response #%d: %s", time.Since(start), numResponsesReturned, err))
				checkers[i] = c
			}
		}
		if len(checkers) == 0 {
			// every checker has passed!
			return syncReq.Since
		}
	}
}

// Perform a single /sync request with the given request options. To sync until something happens,
// see `MustSyncUntil`.
//
// Fails the test if the /sync request does not return 200 OK.
// Returns the top-level parsed /sync response JSON as well as the next_batch token from the response.
func (c *CSAPI) MustSync(t ct.TestLike, syncReq SyncReq) (gjson.Result, string) {
	t.Helper()
	jsonBody, res := c.Sync(t, syncReq)
	mustRespond2xx(t, res)
	return jsonBody, jsonBody.Get("next_batch").Str
}

// Perform a single /sync request with the given request options. To sync until something happens,
// see `MustSyncUntil`.
//
// Always returns the HTTP response, even on non-2xx.
// Returns the top-level parsed /sync response JSON on 2xx.
func (c *CSAPI) Sync(t ct.TestLike, syncReq SyncReq) (gjson.Result, *http.Response) {
	t.Helper()
	query := url.Values{
		"timeout": []string{"1000"},
	}
	// configure the HTTP request based on SyncReq
	if syncReq.TimeoutMillis != "" {
		query["timeout"] = []string{syncReq.TimeoutMillis}
	}
	if syncReq.Since != "" {
		query["since"] = []string{syncReq.Since}
	}
	if syncReq.Filter != "" {
		query["filter"] = []string{syncReq.Filter}
	}
	if syncReq.FullState {
		query["full_state"] = []string{"true"}
	}
	if syncReq.SetPresence != "" {
		query["set_presence"] = []string{syncReq.SetPresence}
	}
	res := c.Do(t, "GET", []string{"_matrix", "client", "v3", "sync"}, WithQueries(query))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return gjson.Result{}, res
	}
	body := ParseJSON(t, res)
	result := gjson.ParseBytes(body)
	return result, res
}

// Check that the timeline for `roomID` has an event which passes the check function.
func SyncTimelineHas(roomID string, check func(gjson.Result) bool) SyncCheckOpt {
	return func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		err := checkArrayElements(
			topLevelSyncJSON, "rooms.join."+GjsonEscape(roomID)+".timeline.events", check,
		)
		if err == nil {
			return nil
		}
		return fmt.Errorf("SyncTimelineHas(%s): %s", roomID, err)
	}
}

// Check that the timeline for `roomID` has an event which matches the event ID.
func SyncTimelineHasEventID(roomID string, eventID string) SyncCheckOpt {
	return SyncTimelineHas(roomID, func(ev gjson.Result) bool {
		return ev.Get("event_id").Str == eventID
	})
}

// Check that the state section for `roomID` has an event which passes the check function.
// Note that the state section of a sync response only contains the change in state up to the start
// of the timeline and will not contain the entire state of the room for incremental or
// `lazy_load_members` syncs.
func SyncStateHas(roomID string, check func(gjson.Result) bool) SyncCheckOpt {
	return func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		err := checkArrayElements(
			topLevelSyncJSON, "rooms.join."+GjsonEscape(roomID)+".state.events", check,
		)
		if err == nil {
			return nil
		}
		return fmt.Errorf("SyncStateHas(%s): %s", roomID, err)
	}
}

func SyncEphemeralHas(roomID string, check func(gjson.Result) bool) SyncCheckOpt {
	return func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		err := checkArrayElements(
			topLevelSyncJSON, "rooms.join."+GjsonEscape(roomID)+".ephemeral.events", check,
		)
		if err == nil {
			return nil
		}
		return fmt.Errorf("SyncEphemeralHas(%s): %s", roomID, err)
	}
}

// Check that the sync contains presence from a user, optionally with an expected presence (set to nil to not check),
// and optionally with extra checks.
func SyncPresenceHas(fromUser string, expectedPresence *string, checks ...func(gjson.Result) bool) SyncCheckOpt {
	return func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		presenceEvents := topLevelSyncJSON.Get("presence.events")
		if !presenceEvents.Exists() {
			return fmt.Errorf("presence.events does not exist")
		}
		for _, x := range presenceEvents.Array() {
			if !(x.Get("type").Exists() &&
				x.Get("sender").Exists() &&
				x.Get("content").Exists() &&
				x.Get("content.presence").Exists()) {
				return fmt.Errorf(
					"malformatted presence event, expected the following fields: [sender, type, content, content.presence]: %s",
					x.Raw,
				)
			} else if x.Get("sender").Str != fromUser {
				continue
			} else if expectedPresence != nil && x.Get("content.presence").Str != *expectedPresence {
				return fmt.Errorf(
					"found presence for user %s, but not expected presence: got %s, want %s",
					fromUser, x.Get("content.presence").Str, *expectedPresence,
				)
			} else {
				for i, check := range checks {
					if !check(x) {
						return fmt.Errorf("matched presence event to user %s, but check %d did not pass", fromUser, i)
					}
				}
				return nil
			}
		}
		return fmt.Errorf("did not find %s in presence events", fromUser)
	}
}

// syncMembershipIn checks that `userID` has `membership` in `roomID`, with optional
// extra checks on the found membership event.
//
// This can be also used to passively observe another user's membership changes in a
// room although we assume that the observing client is joined to the room.
//
// Note: This will not work properly with leave/ban membership for initial syncs, see
// https://github.com/matrix-org/matrix-doc/issues/3537
func syncMembershipIn(userID, roomID, membership string, checks ...func(gjson.Result) bool) SyncCheckOpt {
	checkMembership := func(ev gjson.Result) bool {
		if ev.Get("type").Str == "m.room.member" && ev.Get("state_key").Str == userID && ev.Get("content.membership").Str == membership {
			for _, check := range checks {
				if !check(ev) {
					// short-circuit, bail early
					return false
				}
			}
			// passed both basic membership check and all other checks
			return true
		}
		return false
	}
	return func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		// Check both the timeline and the state events for the membership event since on
		// initial sync, the state events may only be in state. Additionally, state only
		// covers the "updates for the room up to the start of the timeline."

		// We assume the passively observing client user is joined to the room
		roomTypeKey := "join"
		// Otherwise, if the client is the user whose membership we are checking, we need to
		// pick the correct room type JSON key based on the membership being checked.
		if clientUserID == userID {
			if membership == "join" {
				roomTypeKey = "join"
			} else if membership == "leave" || membership == "ban" {
				roomTypeKey = "leave"
			} else if membership == "invite" {
				roomTypeKey = "invite"
			} else if membership == "knock" {
				roomTypeKey = "knock"
			} else {
				return fmt.Errorf("syncMembershipIn(%s, %s): unknown membership: %s", roomID, membership, membership)
			}
		}

		// We assume the passively observing client user is joined to the room (`rooms.join.<roomID>.state`)
		stateKey := "state"
		// Otherwise, if the client is the user whose membership we are checking,
		// we need to pick the correct JSON key based on the membership being checked.
		if clientUserID == userID {
			if membership == "join" || membership == "leave" || membership == "ban" {
				stateKey = "state"
			} else if membership == "invite" {
				stateKey = "invite_state"
			} else if membership == "knock" {
				stateKey = "knock_state"
			} else {
				return fmt.Errorf("syncMembershipIn(%s, %s): unknown membership: %s", roomID, membership, membership)
			}
		}

		// Check the state first as it's a better source of truth than the `timeline`.
		//
		// FIXME: Ideally, we'd use something like `state_after` to get the actual current
		// state in the room instead of us assuming that no state resets/conflicts happen
		// when we apply state from the `timeline` on top of the `state`. But `state_after`
		// is gated behind a sync request parameter which we can't control here.
		firstErr := checkArrayElements(
			topLevelSyncJSON, "rooms."+roomTypeKey+"."+GjsonEscape(roomID)+"."+stateKey+".events", checkMembership,
		)
		if firstErr == nil {
			return nil
		}

		// Check the timeline
		//
		// This is also important to differentiate between leave/ban because those both
		// appear in the `leave` `roomTypeKey` and we need to specifically check the
		// timeline for the membership event to differentiate them.
		var secondErr error
		// The `timeline` is only available for join/leave/ban memberships.
		if slices.Contains([]string{"join", "leave", "ban"}, membership) ||
			// We assume the passively observing client user is joined to the room (therefore
			// has `timeline`).
			clientUserID != userID {
			secondErr = checkArrayElements(
				topLevelSyncJSON, "rooms."+roomTypeKey+"."+GjsonEscape(roomID)+".timeline.events", checkMembership,
			)
			if secondErr == nil {
				return nil
			}
		}

		return fmt.Errorf("syncMembershipIn(%s, %s): %s & %s - %s", roomID, membership, firstErr, secondErr, topLevelSyncJSON)
	}
}

// Checks that `userID` gets invited to `roomID`
//
// Additional checks can be passed to narrow down the check, all must pass.
func SyncInvitedTo(userID, roomID string, checks ...func(gjson.Result) bool) SyncCheckOpt {
	return syncMembershipIn(userID, roomID, "invite", checks...)
}

// Checks that `userID` has knocked on `roomID`
//
// Additional checks can be passed to narrow down the check, all must pass.
func SyncKnockedOn(userID, roomID string, checks ...func(gjson.Result) bool) SyncCheckOpt {
	return syncMembershipIn(userID, roomID, "knock", checks...)
}

// Check that `userID` gets joined to `roomID`
//
// Additional checks can be passed to narrow down the check, all must pass.
func SyncJoinedTo(userID, roomID string, checks ...func(gjson.Result) bool) SyncCheckOpt {
	return syncMembershipIn(userID, roomID, "join", checks...)
}

// Check that `userID` has left the `roomID`
// Note: This will not work properly with initial syncs, see https://github.com/matrix-org/matrix-doc/issues/3537
//
// Additional checks can be passed to narrow down the check, all must pass.
func SyncLeftFrom(userID, roomID string, checks ...func(gjson.Result) bool) SyncCheckOpt {
	return syncMembershipIn(userID, roomID, "leave", checks...)
}

// Check that `userID` is banned from the `roomID`
// Note: This will not work properly with initial syncs, see https://github.com/matrix-org/matrix-doc/issues/3537
//
// Additional checks can be passed to narrow down the check, all must pass.
func SyncBannedFrom(userID, roomID string, checks ...func(gjson.Result) bool) SyncCheckOpt {
	return syncMembershipIn(userID, roomID, "ban", checks...)
}

// Calls the `check` function for each global account data event, and returns with success if the
// `check` function returns true for at least one event.
func SyncGlobalAccountDataHas(check func(gjson.Result) bool) SyncCheckOpt {
	return func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		return checkArrayElements(topLevelSyncJSON, "account_data.events", check)
	}
}

// Calls the `check` function for each account data event for the given room,
// and returns with success if the `check` function returns true for at least
// one event.
func SyncRoomAccountDataHas(roomID string, check func(gjson.Result) bool) SyncCheckOpt {
	return func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		err := checkArrayElements(
			topLevelSyncJSON, "rooms.join."+GjsonEscape(roomID)+".account_data.events", check,
		)
		if err == nil {
			return nil
		}
		return fmt.Errorf("SyncRoomAccountDataHas(%s): %s", roomID, err)
	}
}

// SyncUsersTyping passes when all users in `userIDs` are typing in the same typing EDU.
// It must see a typing EDU first before returning, even if the list of user IDs is empty.
func SyncUsersTyping(roomID string, userIDs []string) SyncCheckOpt {
	// don't sort the input slice the test gave us.
	userIDsCopy := make([]string, len(userIDs))
	copy(userIDsCopy, userIDs)
	sort.Strings(userIDsCopy)
	return SyncEphemeralHas(roomID, func(r gjson.Result) bool {
		if r.Get("type").Str != "m.typing" {
			return false
		}

		var usersSeenTyping []string
		for _, item := range r.Get("content").Get("user_ids").Array() {
			usersSeenTyping = append(usersSeenTyping, item.Str)
		}
		// special case to support nil and 0 length slices
		if len(usersSeenTyping) == 0 && len(userIDsCopy) == 0 {
			return true
		}
		sort.Strings(userIDsCopy)
		sort.Strings(usersSeenTyping)
		return reflect.DeepEqual(userIDsCopy, usersSeenTyping)
	})
}

// Check that sync has received a to-device message,
// with optional user filtering.
//
// If fromUser == "", all messages will be passed through to the check function.
// `check` will be called for all messages that have passed the filter.
//
// `check` gets passed the full event, including sender and type.
func SyncToDeviceHas(fromUser string, check func(gjson.Result) bool) SyncCheckOpt {
	return func(clientUserID string, topLevelSyncJSON gjson.Result) error {
		err := checkArrayElements(
			topLevelSyncJSON, "to_device.events", func(result gjson.Result) bool {
				if fromUser != "" && result.Get("sender").Str != fromUser {
					return false
				} else {
					return check(result)
				}
			},
		)
		if err == nil {
			return nil
		}
		return fmt.Errorf("SyncToDeviceHas(%v): %s", fromUser, err)
	}
}
