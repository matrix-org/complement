package csapi_tests

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/runtime"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
)

// TestPaginationNoDuplicates is an adversarial test that stress-tests /messages
// pagination with small page sizes to catch off-by-one errors in pagination
// token boundaries that only manifest when page boundaries fall mid-sequence.
//
// Background: A real-world bug was observed where the same conduwuit binary
// produced duplicates on Ubuntu 24.04 but not 22.04, because different kernel
// scheduling affected how state events were interleaved with message events,
// which shifted page boundaries. With limit=10 and 20 messages, the bug only
// appeared when state events were distributed in a way that pushed boundaries
// to fall on certain events. Using small limits (1, 3, 7) forces many page
// boundaries and makes the off-by-one virtually guaranteed to trigger if present.
//
// The test creates realistic room activity: topic changes, power level edits,
// joins, leaves, kicks, and reactions interleaved with messages — not just a
// clean sequence of m.room.message events.
func TestMessagesPaginationStressNoDuplicates(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite)

	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
	})
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{
		LocalpartSuffix: "bob",
	})

	// Test with clean messages only (baseline)
	t.Run("Clean messages only", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{
			"preset": "public_chat",
		})

		eventIDs := sendNMessages(t, alice, roomID, 100)

		bob.MustJoinRoom(t, roomID, []spec.ServerName{
			deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
		})

		for _, limit := range []int{1, 3, 7, 50} {
			t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
				if limit == 1 {
					runtime.SkipIf(t, runtime.Synapse)
				}
				assertPaginationIntegrity(t, bob, roomID, eventIDs, limit)
			})
		}
	})

	// Test with messy realistic room activity
	t.Run("Messy room activity", func(t *testing.T) {
		// Register extra local users for join/leave/kick churn
		charlie := deployment.Register(t, "hs1", helpers.RegistrationOpts{
			LocalpartSuffix: "charlie",
		})
		dana := deployment.Register(t, "hs1", helpers.RegistrationOpts{
			LocalpartSuffix: "dana",
		})
		eve := deployment.Register(t, "hs1", helpers.RegistrationOpts{
			LocalpartSuffix: "eve",
		})

		roomID := alice.MustCreateRoom(t, map[string]interface{}{
			"preset": "public_chat",
		})

		// Track only the message event IDs we care about verifying
		var trackedEventIDs []string

		// --- Phase 1: Initial chatter with topic changes ---
		trackedEventIDs = append(trackedEventIDs, sendNMessages(t, alice, roomID, 10)...)

		// Change topic
		alice.SendEventSynced(t, roomID, b.Event{
			Type:     "m.room.topic",
			StateKey: b.Ptr(""),
			Content: map[string]interface{}{
				"topic": "Phase 1: Getting started",
			},
		})

		trackedEventIDs = append(trackedEventIDs, sendNMessages(t, alice, roomID, 5)...)

		// --- Phase 2: Users join mid-conversation ---
		charlie.MustJoinRoom(t, roomID, nil)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(charlie.UserID, roomID))

		trackedEventIDs = append(trackedEventIDs, sendNMessages(t, alice, roomID, 5)...)
		trackedEventIDs = append(trackedEventIDs, sendNMessages(t, charlie, roomID, 5)...)

		dana.MustJoinRoom(t, roomID, nil)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(dana.UserID, roomID))

		// Change topic again
		alice.SendEventSynced(t, roomID, b.Event{
			Type:     "m.room.topic",
			StateKey: b.Ptr(""),
			Content: map[string]interface{}{
				"topic": "Phase 2: More people joining",
			},
		})

		trackedEventIDs = append(trackedEventIDs, sendNMessages(t, alice, roomID, 5)...)
		trackedEventIDs = append(trackedEventIDs, sendNMessages(t, dana, roomID, 3)...)

		// --- Phase 3: Power level changes ---
		// Give charlie moderator power
		alice.SendEventSynced(t, roomID, b.Event{
			Type:     "m.room.power_levels",
			StateKey: b.Ptr(""),
			Content: map[string]interface{}{
				"users": powerLevelUsersForRoomVersion(t, alice.GetDefaultRoomVersion(t), alice.UserID, map[string]interface{}{
					alice.UserID:   100,
					charlie.UserID: 50,
				}),
			},
		})

		trackedEventIDs = append(trackedEventIDs, sendNMessages(t, charlie, roomID, 5)...)

		// --- Phase 4: User leaves and rejoins ---
		dana.MustLeaveRoom(t, roomID)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(dana.UserID, roomID))

		trackedEventIDs = append(trackedEventIDs, sendNMessages(t, alice, roomID, 10)...)

		// Dana rejoins
		dana.MustJoinRoom(t, roomID, nil)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(dana.UserID, roomID))

		trackedEventIDs = append(trackedEventIDs, sendNMessages(t, dana, roomID, 5)...)

		// --- Phase 5: Kick a user ---
		eve.MustJoinRoom(t, roomID, nil)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(eve.UserID, roomID))

		trackedEventIDs = append(trackedEventIDs, sendNMessages(t, eve, roomID, 3)...)

		// Alice kicks eve
		alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "kick"},
			client.WithJSONBody(t, map[string]interface{}{
				"user_id": eve.UserID,
				"reason":  "Testing kick pagination",
			}),
		)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(eve.UserID, roomID))

		trackedEventIDs = append(trackedEventIDs, sendNMessages(t, alice, roomID, 5)...)

		// --- Phase 6: More topic changes and messages to pad out ---
		alice.SendEventSynced(t, roomID, b.Event{
			Type:     "m.room.topic",
			StateKey: b.Ptr(""),
			Content: map[string]interface{}{
				"topic": "Phase 6: Final stretch",
			},
		})

		trackedEventIDs = append(trackedEventIDs, sendNMessages(t, alice, roomID, 10)...)
		trackedEventIDs = append(trackedEventIDs, sendNMessages(t, charlie, roomID, 5)...)

		// Change room name for good measure
		alice.SendEventSynced(t, roomID, b.Event{
			Type:     "m.room.name",
			StateKey: b.Ptr(""),
			Content: map[string]interface{}{
				"name": "Stress Test Room",
			},
		})

		trackedEventIDs = append(trackedEventIDs, sendNMessages(t, alice, roomID, 10)...)
		trackedEventIDs = append(trackedEventIDs, sendNMessages(t, dana, roomID, 5)...)

		// --- Phase 7: Reactions (custom events) interleaved ---
		// Send some reactions to earlier messages
		for i := 0; i < 5 && i < len(trackedEventIDs); i++ {
			alice.SendEventSynced(t, roomID, b.Event{
				Type: "m.reaction",
				Content: map[string]interface{}{
					"m.relates_to": map[string]interface{}{
						"rel_type": "m.annotation",
						"event_id": trackedEventIDs[i],
						"key":      "👍",
					},
				},
			})
		}

		trackedEventIDs = append(trackedEventIDs, sendNMessages(t, alice, roomID, 9)...)

		t.Logf("Total tracked message events: %d", len(trackedEventIDs))
		t.Logf("Room should also contain: ~5 creation events, 3 topic changes, " +
			"1 room name, 1 power level change, ~8 membership changes, 5 reactions = " +
			"~23 non-message events interspersed throughout the timeline")

		// Now bob joins from a federated server and paginates through all of it
		bob.MustJoinRoom(t, roomID, []spec.ServerName{
			deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
		})

		for _, limit := range []int{1, 3, 7, 50} {
			t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
				if limit == 1 {
					runtime.SkipIf(t, runtime.Synapse)
				}
				assertPaginationIntegrity(t, bob, roomID, trackedEventIDs, limit)
			})
		}
	})

	// Test re-join scenario (the one that originally failed)
	t.Run("Re-join with activity during absence", func(t *testing.T) {
		roomID := alice.MustCreateRoom(t, map[string]interface{}{
			"preset": "public_chat",
		})

		// Bob joins then leaves
		bob.MustJoinRoom(t, roomID, []spec.ServerName{
			deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
		})
		bob.MustLeaveRoom(t, roomID)
		alice.MustSyncUntil(t, client.SyncReq{},
			client.SyncLeftFrom(bob.UserID, roomID))

		// While bob is away: messages + state changes
		var trackedEventIDs []string
		trackedEventIDs = append(trackedEventIDs, sendNMessages(t, alice, roomID, 30)...)

		alice.SendEventSynced(t, roomID, b.Event{
			Type:     "m.room.topic",
			StateKey: b.Ptr(""),
			Content: map[string]interface{}{
				"topic": "Bob missed this topic change",
			},
		})

		trackedEventIDs = append(trackedEventIDs, sendNMessages(t, alice, roomID, 30)...)

		alice.SendEventSynced(t, roomID, b.Event{
			Type:     "m.room.topic",
			StateKey: b.Ptr(""),
			Content: map[string]interface{}{
				"topic": "And this one too",
			},
		})

		trackedEventIDs = append(trackedEventIDs, sendNMessages(t, alice, roomID, 40)...)

		// Bob re-joins
		bob.MustJoinRoom(t, roomID, []spec.ServerName{
			deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
		})

		for _, limit := range []int{1, 3, 7, 50} {
			t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
				if limit == 1 {
					runtime.SkipIf(t, runtime.Synapse)
				}
				assertPaginationIntegrity(t, bob, roomID, trackedEventIDs, limit)
			})
		}
	})
}

// TestPaginationForwardAndJumpToStart tests forward pagination (dir=f) and
// the real-world scenario of a client jumping to the room's creation event
// and scrolling downward. This exercises different code paths than backward
// pagination — forward tokens, forward ordering, and the interaction between
// "find the oldest token" and "paginate forward from it".
func TestMessagesPaginationStressForwardAndJumpToStart(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite)

	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
	})
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{
		LocalpartSuffix: "bob",
	})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	eventIDs := sendNMessages(t, alice, roomID, 100)

	bob.MustJoinRoom(t, roomID, []spec.ServerName{
		deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
	})

	// Test pure forward pagination from the start
	t.Run("Forward from start", func(t *testing.T) {
		// Derive a real start token by paginating backward to the beginning
		startToken := findRoomStartToken(t, bob, roomID)
		for _, limit := range []int{1, 3, 7, 50} {
			t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
				assertPaginationIntegrityWithDirFrom(t, bob, roomID, eventIDs, limit, "f", startToken)
			})
		}
	})

	// Test backward pagination too for comparison
	t.Run("Backward from end", func(t *testing.T) {
		for _, limit := range []int{1, 3, 7, 50} {
			t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
				assertPaginationIntegrityWithDir(t, bob, roomID, eventIDs, limit, "b")
			})
		}
	})

	// The real adversarial scenario: paginate backward halfway, then jump to
	// the room creation event and scroll forward from there.
	// This simulates a user reading recent messages, then clicking "jump to
	// beginning" and scrolling down — a common Element/client behavior.
	t.Run("Jump to start mid-pagination then scroll forward", func(t *testing.T) {
		for _, limit := range []int{3, 7} {
			t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
				// Step 1: Paginate backwards a few pages (simulating reading recent msgs)
				var backwardEventIDs []string
				fromToken := ""
				pagesBack := 3

				for page := 0; page < pagesBack; page++ {
					queryParams := url.Values{
						"dir":   []string{"b"},
						"limit": []string{strconv.Itoa(limit)},
					}
					if fromToken != "" {
						queryParams.Set("from", fromToken)
					}

					res := bob.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"},
						client.WithContentType("application/json"),
						client.WithQueries(queryParams),
					)
					body := client.ParseJSON(t, res)

					for _, event := range gjson.GetBytes(body, "chunk").Array() {
						backwardEventIDs = append(backwardEventIDs, event.Get("event_id").Str)
					}

					endToken := gjson.GetBytes(body, "end")
					if !endToken.Exists() {
						break
					}
					fromToken = endToken.Str
				}

				t.Logf("Backward phase: collected %d events over %d pages", len(backwardEventIDs), pagesBack)

				// Step 2: Now jump to the very start of the room.
				// To get the "start" token, paginate backward all the way to get the
				// final `end` token (which points to the room start).
				startToken := ""
				scanToken := ""
				for i := 0; i < 500; i++ {
					queryParams := url.Values{
						"dir":   []string{"b"},
						"limit": []string{"100"}, // use large limit to get there fast
					}
					if scanToken != "" {
						queryParams.Set("from", scanToken)
					}
					res := bob.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"},
						client.WithContentType("application/json"),
						client.WithQueries(queryParams),
					)
					body := client.ParseJSON(t, res)

					endToken := gjson.GetBytes(body, "end")
					if !endToken.Exists() {
						// We've reached the start; use the `start` token from this
						// response as our forward starting point
						startTokenRes := gjson.GetBytes(body, "start")
						if startTokenRes.Exists() {
							startToken = startTokenRes.Str
						}
						break
					}
					scanToken = endToken.Str
					// The last valid `end` token before we hit the wall
					startToken = endToken.Str
				}

				if startToken == "" {
					t.Fatal("could not find start token for room")
				}

				t.Logf("Found room start token: %s", startToken)

				// Step 3: Now paginate FORWARD from the start token
				var forwardEventIDs []string
				var forwardTypes []string
				fromToken = startToken
				requestCount := 0

				for {
					queryParams := url.Values{
						"dir":   []string{"f"},
						"limit": []string{strconv.Itoa(limit)},
						"from":  []string{fromToken},
					}

					res := bob.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"},
						client.WithContentType("application/json"),
						client.WithQueries(queryParams),
					)
					body := client.ParseJSON(t, res)
					requestCount++

					for _, event := range gjson.GetBytes(body, "chunk").Array() {
						forwardEventIDs = append(forwardEventIDs, event.Get("event_id").Str)
						forwardTypes = append(forwardTypes, event.Get("type").Str)
					}

					endToken := gjson.GetBytes(body, "end")
					if !endToken.Exists() {
						break
					}
					fromToken = endToken.Str

					if requestCount > 500 {
						t.Fatalf("forward pagination did not terminate after %d requests", requestCount)
					}
				}

				t.Logf("Forward phase: collected %d events over %d pages", len(forwardEventIDs), requestCount)

				// CHECK: No duplicates in forward pagination
				seen := make(map[string]int)
				var duplicates []string
				for i, eventID := range forwardEventIDs {
					if firstIdx, exists := seen[eventID]; exists {
						duplicates = append(duplicates, fmt.Sprintf(
							"  %s at positions %d and %d (type: %s)",
							eventID, firstIdx, i, forwardTypes[i],
						))
					} else {
						seen[eventID] = i
					}
				}
				if len(duplicates) > 0 {
					shown := duplicates
					if len(shown) > 20 {
						shown = shown[:20]
						shown = append(shown, fmt.Sprintf("  ... and %d more", len(duplicates)-20))
					}
					t.Errorf("FORWARD PAGINATION DUPLICATES (%d):\n%s",
						len(duplicates), strings.Join(shown, "\n"))
				}

				// CHECK: All expected messages present in forward scan
				var missing []string
				for i, expectedID := range eventIDs {
					if _, exists := seen[expectedID]; !exists {
						missing = append(missing, fmt.Sprintf("  message %d: %s", i, expectedID))
					}
				}
				if len(missing) > 0 {
					shown := missing
					if len(shown) > 20 {
						shown = shown[:20]
						shown = append(shown, fmt.Sprintf("  ... and %d more", len(missing)-20))
					}
					t.Errorf("FORWARD PAGINATION MISSING (%d of %d):\n%s",
						len(missing), len(eventIDs), strings.Join(shown, "\n"))
				}

				// CHECK: Forward order should be chronological (not reversed)
				var forwardMsgIDs []string
				forwardSeen := make(map[string]bool)
				for i, eventID := range forwardEventIDs {
					if forwardTypes[i] == "m.room.message" && !forwardSeen[eventID] {
						forwardMsgIDs = append(forwardMsgIDs, eventID)
						forwardSeen[eventID] = true
					}
				}

				minLen := len(forwardMsgIDs)
				if len(eventIDs) < minLen {
					minLen = len(eventIDs)
				}
				for i := 0; i < minLen; i++ {
					if forwardMsgIDs[i] != eventIDs[i] {
						t.Errorf("FORWARD ORDER MISMATCH at position %d: got %s, want %s",
							i, forwardMsgIDs[i], eventIDs[i])
						break
					}
				}
			})
		}
	})
}

// TestMessagesPaginationStressStaleTokenResume simulates a client that paginates
// partway through a room, closes the app (goes offline), and comes back later
// after significant room activity has occurred. The client resumes pagination
// from a saved `end` token. This tests whether pagination tokens remain valid
// and produce correct results after the timeline has been mutated.
//
// During the "away" period, the room gets:
//   - Messages from alice (local sender on hs1)
//   - Messages from a new federated user on hs2 (received events)
//   - Membership changes: new users joining and leaving
//   - State changes: topic updates
//
// This catches bugs where:
//   - Old pagination tokens are silently invalidated by new events
//   - Gaps appear between the "pre-away" and "post-away" pagination results
//   - Duplicates appear at the token boundary
//   - New membership/state events confuse the token position
func TestMessagesPaginationStressStaleTokenResume(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite)

	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "alice",
	})
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{
		LocalpartSuffix: "bob",
	})
	charlie := deployment.Register(t, "hs1", helpers.RegistrationOpts{
		LocalpartSuffix: "charlie",
	})
	dana := deployment.Register(t, "hs2", helpers.RegistrationOpts{
		LocalpartSuffix: "dana",
	})

	for _, limit := range []int{3, 7} {
		t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
			// Fresh room per limit for full isolation
			roomID := alice.MustCreateRoom(t, map[string]interface{}{
				"preset": "public_chat",
			})

			// Bob joins the room (federated)
			bob.MustJoinRoom(t, roomID, []spec.ServerName{
				deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
			})

			// === PRE-AWAY PHASE: Initial room activity ===
			var allTrackedEventIDs []string
			preAwayEventIDs := sendNMessages(t, alice, roomID, 30)
			allTrackedEventIDs = append(allTrackedEventIDs, preAwayEventIDs...)

			// Bob sends some messages too (so hs2 has both sent and received events)
			bobPreAwayIDs := sendNMessages(t, bob, roomID, 10)
			allTrackedEventIDs = append(allTrackedEventIDs, bobPreAwayIDs...)

			// More from alice
			morePreAway := sendNMessages(t, alice, roomID, 10)
			allTrackedEventIDs = append(allTrackedEventIDs, morePreAway...)

			// The stale token should represent a client that has caught up to the
			// pre-away timeline. Otherwise later federation delivery can make
			// pre-away events appear newer than the saved local cursor.
			bob.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, morePreAway[len(morePreAway)-1]))

			t.Logf("Pre-away phase: %d tracked messages", len(allTrackedEventIDs))

			// === BOB PAGINATES BACKWARDS PARTWAY ===
			// Simulate reading recent messages before closing the app
			var preAwayCollected []string
			var preAwayTypes []string
			savedToken := ""
			pagesRead := 0

			fromToken := ""
			// Read ~3 pages worth
			for page := 0; page < 4; page++ {
				queryParams := url.Values{
					"dir":   []string{"b"},
					"limit": []string{strconv.Itoa(limit)},
				}
				if fromToken != "" {
					queryParams.Set("from", fromToken)
				}

				res := bob.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"},
					client.WithContentType("application/json"),
					client.WithQueries(queryParams),
				)
				body := client.ParseJSON(t, res)

				for _, event := range gjson.GetBytes(body, "chunk").Array() {
					preAwayCollected = append(preAwayCollected, event.Get("event_id").Str)
					preAwayTypes = append(preAwayTypes, event.Get("type").Str)
				}

				endToken := gjson.GetBytes(body, "end")
				if !endToken.Exists() {
					break
				}
				fromToken = endToken.Str
				pagesRead++
			}

			// Save the token — this is what the client stores before going offline
			savedToken = fromToken

			t.Logf("Bob read %d pages (%d events) before going 'offline'. Saved token: %s",
				pagesRead, len(preAwayCollected), savedToken)

			if savedToken == "" {
				t.Fatal("no saved token — room too small to partially paginate")
			}

			// Snapshot the pre-away message IDs for verification.
			// Backward pagination from the stale token goes further into the past,
			// so away-phase messages (which are newer) won't appear.
			preAwayMessageIDs := make([]string, len(allTrackedEventIDs))
			copy(preAwayMessageIDs, allTrackedEventIDs)

			// === WHILE BOB IS "AWAY": Messy room activity ===
			// Alice sends more messages (local events on hs1)
			awayAliceMsgs := sendNMessages(t, alice, roomID, 15)

			// Charlie joins (new local user, membership event)
			charlie.MustJoinRoom(t, roomID, nil)
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(charlie.UserID, roomID))

			// Charlie sends messages
			awayCharlieMsgs := sendNMessages(t, charlie, roomID, 5)

			// Topic change
			alice.SendEventSynced(t, roomID, b.Event{
				Type:     "m.room.topic",
				StateKey: b.Ptr(""),
				Content: map[string]interface{}{
					"topic": "Bob missed this while offline",
				},
			})

			// Dana joins from hs2 (federated membership event)
			dana.MustJoinRoom(t, roomID, []spec.ServerName{
				deployment.GetFullyQualifiedHomeserverName(t, "hs1"),
			})
			dana.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(dana.UserID, roomID))
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(dana.UserID, roomID))
			// Ensure dana leaves even if the test fails partway through
			defer dana.MustLeaveRoom(t, roomID)

			// Dana sends messages (federated received events on hs1)
			awayDanaMsgs := sendNMessages(t, dana, roomID, 5)

			// Charlie leaves
			charlie.MustLeaveRoom(t, roomID)
			alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(charlie.UserID, roomID))

			// More alice messages after the churn
			awayMoreAlice := sendNMessages(t, alice, roomID, 10)

			t.Logf("While-away phase: added %d more tracked messages + membership/state events",
				len(awayAliceMsgs)+len(awayCharlieMsgs)+len(awayDanaMsgs)+len(awayMoreAlice))

			// === BOB COMES BACK: Resume pagination from stale token ===
			var resumeCollected []string
			var resumeTypes []string
			fromToken = savedToken
			requestCount := 0

			for {
				queryParams := url.Values{
					"dir":   []string{"b"},
					"limit": []string{strconv.Itoa(limit)},
					"from":  []string{fromToken},
				}

				res := bob.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"},
					client.WithContentType("application/json"),
					client.WithQueries(queryParams),
				)
				body := client.ParseJSON(t, res)
				requestCount++

				for _, event := range gjson.GetBytes(body, "chunk").Array() {
					resumeCollected = append(resumeCollected, event.Get("event_id").Str)
					resumeTypes = append(resumeTypes, event.Get("type").Str)
				}

				endToken := gjson.GetBytes(body, "end")
				if !endToken.Exists() {
					break
				}
				fromToken = endToken.Str

				if requestCount > 500 {
					t.Fatalf("resume pagination did not terminate after %d requests", requestCount)
				}
			}

			t.Logf("Resume phase: collected %d events over %d pages", len(resumeCollected), requestCount)

			// === VERIFICATION ===
			// Combine pre-away + resume into the full set
			allCollected := append(preAwayCollected, resumeCollected...)
			allTypes := append(preAwayTypes, resumeTypes...)

			// CHECK 1: No duplicates across the entire session
			seen := make(map[string]int)
			var duplicates []string
			for i, eventID := range allCollected {
				if firstIdx, exists := seen[eventID]; exists {
					duplicates = append(duplicates, fmt.Sprintf(
						"  %s at positions %d and %d (type: %s) [%s]",
						eventID, firstIdx, i, allTypes[i],
						func() string {
							if firstIdx < len(preAwayCollected) && i >= len(preAwayCollected) {
								return "CROSS-BOUNDARY: appeared in pre-away AND resume"
							}
							if i < len(preAwayCollected) {
								return "within pre-away"
							}
							return "within resume"
						}(),
					))
				} else {
					seen[eventID] = i
				}
			}
			if len(duplicates) > 0 {
				shown := duplicates
				if len(shown) > 20 {
					shown = shown[:20]
					shown = append(shown, fmt.Sprintf("  ... and %d more", len(duplicates)-20))
				}
				t.Errorf("STALE TOKEN RESUME: DUPLICATES (%d) across pre-away + resume:\n%s",
					len(duplicates), strings.Join(shown, "\n"))
			}

			// CHECK 2: All pre-away messages present somewhere.
			// Only check pre-away messages since backward pagination from the
			// stale token goes further into the past and won't see newer events.
			var missingPreAway []string
			for i, expectedID := range preAwayMessageIDs {
				if _, exists := seen[expectedID]; !exists {
					missingPreAway = append(missingPreAway, fmt.Sprintf("  pre-away message %d: %s", i, expectedID))
				}
			}
			if len(missingPreAway) > 0 {
				shown := missingPreAway
				if len(shown) > 20 {
					shown = shown[:20]
					shown = append(shown, fmt.Sprintf("  ... and %d more", len(missingPreAway)-20))
				}
				t.Errorf("STALE TOKEN RESUME: MISSING PRE-AWAY EVENTS (%d of %d):\n%s",
					len(missingPreAway), len(preAwayMessageIDs), strings.Join(shown, "\n"))
			}

			// CHECK 3: Event type breakdown for diagnostics
			typeCounts := make(map[string]int)
			for _, eventType := range allTypes {
				typeCounts[eventType]++
			}
			var typeReport []string
			for eventType, count := range typeCounts {
				typeReport = append(typeReport, fmt.Sprintf("%s: %d", eventType, count))
			}
			t.Logf("Combined event type breakdown: %s", strings.Join(typeReport, ", "))
		})
	}
}

// TestPaginationTokenStability verifies that paginating the same room with
// different limit values always yields the same complete, ordered set of
// events. This catches bugs where pagination tokens encode limit-dependent
// state that breaks when clients retry with different parameters.
func TestMessagesPaginationStressTokenStability(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite)

	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	eventIDs := sendNMessages(t, alice, roomID, 50)

	// Paginate with multiple limits and compare results
	var referenceEventIDs []string

	for i, limit := range []int{1, 5, 10, 25, 100} {
		t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
			result := collectAllMessageEventIDs(t, alice, roomID, limit)

			if i == 0 {
				referenceEventIDs = result
				t.Logf("Reference pagination (limit=%d): %d events", limit, len(result))
			} else {
				// Every limit should produce the same set of events in the same order
				if len(result) != len(referenceEventIDs) {
					t.Fatalf("limit=%d produced %d events, but limit=1 produced %d events",
						limit, len(result), len(referenceEventIDs))
				}
				for j, eventID := range result {
					if eventID != referenceEventIDs[j] {
						t.Fatalf("limit=%d event at index %d differs from limit=1: got %s, want %s",
							limit, j, eventID, referenceEventIDs[j])
					}
				}
			}
		})
	}

	// Also verify all sent messages are present in the reference
	for _, eventID := range eventIDs {
		if !slices.Contains(referenceEventIDs, eventID) {
			t.Errorf("sent event %s not found in any pagination result", eventID)
		}
	}
}

// sendNMessages sends n messages into a room.
//
// We use unsynced sends here and then wait periodically for the latest event
// to appear in /sync. That keeps the test behavior close to the old helper
// while avoiding an expensive sync round-trip per message.
// Returns the event IDs in send order.
func sendNMessages(t *testing.T, sender *client.CSAPI, roomID string, n int) []string {
	t.Helper()

	eventIDs := make([]string, n)
	const syncEvery = 10
	for i := 0; i < n; i++ {
		eventIDs[i] = sender.Unsafe_SendEventUnsynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    fmt.Sprintf("Message %d of %d", i+1, n),
			},
		})

		if (i+1)%syncEvery == 0 || i+1 == n {
			sender.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, eventIDs[i]))
		}
	}

	t.Logf("Sent %d messages into room %s", n, roomID)
	return eventIDs
}

func powerLevelUsersForRoomVersion(t *testing.T, roomVersion gomatrixserverlib.RoomVersion, creator string, users map[string]interface{}) map[string]interface{} {
	t.Helper()

	if gomatrixserverlib.MustGetRoomVersion(roomVersion).PrivilegedCreators() {
		delete(users, creator)
	}

	return users
}

// paginationResult holds the raw results of paginating through a room.
type paginationResult struct {
	// All event IDs collected (may include duplicates)
	allEventIDs []string
	// All event types collected (parallel to allEventIDs)
	allEventTypes []string
	// Number of /messages requests made
	requestCount int
	// Events per page (for diagnostics)
	eventsPerPage []int
}

// findRoomStartToken paginates backward through a room with large pages to find
// the token pointing to the very start of the room timeline. This token can then
// be used as a starting point for forward (dir=f) pagination.
func findRoomStartToken(t *testing.T, user *client.CSAPI, roomID string) string {
	t.Helper()

	startToken := ""
	scanToken := ""
	for i := 0; i < 500; i++ {
		queryParams := url.Values{
			"dir":   []string{"b"},
			"limit": []string{"100"},
		}
		if scanToken != "" {
			queryParams.Set("from", scanToken)
		}
		res := user.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"},
			client.WithContentType("application/json"),
			client.WithQueries(queryParams),
		)
		body := client.ParseJSON(t, res)

		endToken := gjson.GetBytes(body, "end")
		if !endToken.Exists() {
			// Reached the start; use the `start` token from this response
			startTokenRes := gjson.GetBytes(body, "start")
			if startTokenRes.Exists() {
				startToken = startTokenRes.Str
			}
			break
		}
		scanToken = endToken.Str
		startToken = endToken.Str
	}

	if startToken == "" {
		t.Fatal("could not find start token for room")
	}

	t.Logf("Found room start token: %s", startToken)
	return startToken
}

// paginateRoom paginates through a room's /messages endpoint in the given
// direction ("b" for backwards, "f" for forwards), collecting ALL events
// (including state events) without any filtering.
func paginateRoom(t *testing.T, user *client.CSAPI, roomID string, limit int) paginationResult {
	t.Helper()
	return paginateRoomDirFrom(t, user, roomID, limit, "b", "")
}

func paginateRoomDir(t *testing.T, user *client.CSAPI, roomID string, limit int, dir string) paginationResult {
	t.Helper()
	return paginateRoomDirFrom(t, user, roomID, limit, dir, "")
}

func paginateRoomDirFrom(t *testing.T, user *client.CSAPI, roomID string, limit int, dir string, initialToken string) paginationResult {
	t.Helper()

	result := paginationResult{}
	fromToken := initialToken

	for {
		messageQueryParams := url.Values{
			"dir":   []string{dir},
			"limit": []string{strconv.Itoa(limit)},
		}
		if fromToken != "" {
			messageQueryParams.Set("from", fromToken)
		}

		messagesRes := user.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"},
			client.WithContentType("application/json"),
			client.WithQueries(messageQueryParams),
		)
		messagesResBody := client.ParseJSON(t, messagesRes)
		result.requestCount++

		chunkRes := gjson.GetBytes(messagesResBody, "chunk")
		if !chunkRes.Exists() || !chunkRes.IsArray() {
			t.Fatalf("page %d: missing or non-array 'chunk' in /messages response", result.requestCount)
		}

		pageEvents := chunkRes.Array()
		result.eventsPerPage = append(result.eventsPerPage, len(pageEvents))

		for _, event := range pageEvents {
			result.allEventIDs = append(result.allEventIDs, event.Get("event_id").Str)
			result.allEventTypes = append(result.allEventTypes, event.Get("type").Str)
		}

		// Paginate until no `end` token (reached the boundary)
		endTokenRes := gjson.GetBytes(messagesResBody, "end")
		if !endTokenRes.Exists() {
			break
		}
		fromToken = endTokenRes.Str

		// Safety valve: don't loop forever
		if result.requestCount > 500 {
			t.Fatalf("pagination did not terminate after %d requests", result.requestCount)
		}
	}

	return result
}

// collectAllMessageEventIDs paginates a room and returns only the m.room.message
// event IDs in chronological order (reversed from the backwards pagination).
func collectAllMessageEventIDs(t *testing.T, user *client.CSAPI, roomID string, limit int) []string {
	t.Helper()

	result := paginateRoom(t, user, roomID, limit)

	// Filter to just message events, preserving exact pagination output.
	var messageEventIDs []string
	for i, eventID := range result.allEventIDs {
		if result.allEventTypes[i] == "m.room.message" {
			messageEventIDs = append(messageEventIDs, eventID)
		}
	}

	// Reverse to chronological order (pagination is backwards)
	slices.Reverse(messageEventIDs)
	return messageEventIDs
}

// assertPaginationIntegrity paginates a room backwards and checks integrity.
func assertPaginationIntegrity(
	t *testing.T,
	user *client.CSAPI,
	roomID string,
	expectedMessageEventIDs []string,
	limit int,
) {
	t.Helper()
	assertPaginationIntegrityWithDirFrom(t, user, roomID, expectedMessageEventIDs, limit, "b", "")
}

// assertPaginationIntegrityWithDir paginates a room in the given direction and
// checks three independent properties:
//  1. NO DUPLICATES: no event_id appears more than once across all pages
//  2. NO GAPS: every expected message event_id is present
//  3. CORRECT ORDER: message events appear in the expected chronological order
//
// It also reports on non-message event types for diagnostic purposes.
func assertPaginationIntegrityWithDir(
	t *testing.T,
	user *client.CSAPI,
	roomID string,
	expectedMessageEventIDs []string,
	limit int,
	dir string,
) {
	t.Helper()
	assertPaginationIntegrityWithDirFrom(t, user, roomID, expectedMessageEventIDs, limit, dir, "")
}

// assertPaginationIntegrityWithDirFrom is the same as assertPaginationIntegrityWithDir
// but accepts an initial pagination token (e.g. a start-of-room token for forward pagination).
func assertPaginationIntegrityWithDirFrom(
	t *testing.T,
	user *client.CSAPI,
	roomID string,
	expectedMessageEventIDs []string,
	limit int,
	dir string,
	initialToken string,
) {
	t.Helper()

	result := paginateRoomDirFrom(t, user, roomID, limit, dir, initialToken)

	t.Logf("Paginated with limit=%d: %d requests, %d total events, pages: %v",
		limit, result.requestCount, len(result.allEventIDs), result.eventsPerPage)

	// =====================================================================
	// CHECK 1: No duplicate events across pages
	// =====================================================================
	seen := make(map[string]int) // event_id -> first occurrence index
	var duplicates []string
	for i, eventID := range result.allEventIDs {
		if firstIdx, exists := seen[eventID]; exists {
			duplicates = append(duplicates, fmt.Sprintf(
				"  %s appeared at positions %d and %d (type: %s)",
				eventID, firstIdx, i, result.allEventTypes[i],
			))
		} else {
			seen[eventID] = i
		}
	}
	if len(duplicates) > 0 {
		// Show at most 20 duplicates to avoid flooding
		shown := duplicates
		if len(shown) > 20 {
			shown = shown[:20]
			shown = append(shown, fmt.Sprintf("  ... and %d more", len(duplicates)-20))
		}
		t.Errorf("DUPLICATE EVENTS DETECTED (%d duplicates across %d pages with limit=%d):\n%s",
			len(duplicates), result.requestCount, limit, strings.Join(shown, "\n"))
	}

	// =====================================================================
	// CHECK 2: No missing message events
	// =====================================================================
	var missing []string
	for i, expectedID := range expectedMessageEventIDs {
		if _, exists := seen[expectedID]; !exists {
			missing = append(missing, fmt.Sprintf("  message %d: %s", i, expectedID))
		}
	}
	if len(missing) > 0 {
		// Show at most 20 missing to avoid flooding
		shown := missing
		if len(shown) > 20 {
			shown = shown[:20]
			shown = append(shown, fmt.Sprintf("  ... and %d more", len(missing)-20))
		}
		t.Errorf("MISSING EVENTS (%d of %d messages not found with limit=%d):\n%s",
			len(missing), len(expectedMessageEventIDs), limit, strings.Join(shown, "\n"))
	}

	// =====================================================================
	// CHECK 3: Correct order
	// Backward pagination returns reverse-chronological; forward returns chronological
	// =====================================================================
	var messageEventsInPaginationOrder []string
	paginationSeen := make(map[string]bool)
	for i, eventID := range result.allEventIDs {
		if result.allEventTypes[i] == "m.room.message" && !paginationSeen[eventID] {
			messageEventsInPaginationOrder = append(messageEventsInPaginationOrder, eventID)
			paginationSeen[eventID] = true
		}
	}

	// Convert to chronological order for comparison
	chronological := slices.Clone(messageEventsInPaginationOrder)
	if dir == "b" {
		slices.Reverse(chronological)
	}
	// If dir == "f", it's already chronological

	// Find first out-of-order event
	minLen := len(chronological)
	if len(expectedMessageEventIDs) < minLen {
		minLen = len(expectedMessageEventIDs)
	}
	for i := 0; i < minLen; i++ {
		if chronological[i] != expectedMessageEventIDs[i] {
			t.Errorf("ORDER MISMATCH at position %d (limit=%d): got %s, want %s",
				i, limit, chronological[i], expectedMessageEventIDs[i])
			break
		}
	}

	// =====================================================================
	// DIAGNOSTIC: Report on non-message event types seen
	// =====================================================================
	typeCounts := make(map[string]int)
	for _, eventType := range result.allEventTypes {
		typeCounts[eventType]++
	}
	var typeReport []string
	for eventType, count := range typeCounts {
		typeReport = append(typeReport, fmt.Sprintf("%s: %d", eventType, count))
	}
	t.Logf("Event type breakdown: %s", strings.Join(typeReport, ", "))
}

// dumpEventDetails is a test helper that can be called to log all raw events from
// pagination for debugging purposes. This is intentionally verbose.
func dumpEventDetails(t *testing.T, messagesResBody json.RawMessage, pageNum int) {
	t.Helper()

	chunkRes := gjson.GetBytes(messagesResBody, "chunk")
	if !chunkRes.Exists() {
		return
	}

	for i, event := range chunkRes.Array() {
		t.Logf("  Page %d, event %d: type=%s event_id=%s state_key=%s",
			pageNum, i,
			event.Get("type").Str,
			event.Get("event_id").Str,
			event.Get("state_key").Str,
		)
	}
}
