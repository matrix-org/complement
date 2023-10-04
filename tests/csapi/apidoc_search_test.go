package csapi_tests

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/matrix-org/util"
	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

// Note: In contrast to Sytest, we define a filter.rooms on each search request, this is to mimic
// creating a new user and new room per test. This also allows us to run in parallel.
func TestSearch(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	t.Run("parallel", func(t *testing.T) {
		// sytest: Can search for an event by body
		t.Run("Can search for an event by body", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})
			eventID := alice.SendEventSynced(t, roomID, b.Event{
				Type: "m.room.message",
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    "hello, world",
				},
			})

			searchRequest := client.WithJSONBody(t, map[string]interface{}{
				"search_categories": map[string]interface{}{
					"room_events": map[string]interface{}{
						"keys":        []string{"content.body"},
						"search_term": "hello",
						"filter": map[string]interface{}{
							"rooms": []string{roomID},
						},
					},
				},
			})

			resp := alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "search"}, searchRequest)
			sce := "search_categories.room_events"
			result0 := sce + ".results.0.result"
			must.MatchResponse(t, resp, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONKeyPresent(sce + ".count"),
					match.JSONKeyPresent(sce + ".results"),
					match.JSONKeyEqual(sce+".count", float64(1)),
					match.JSONKeyEqual(result0+".event_id", eventID),
					match.JSONKeyEqual(result0+".room_id", roomID),
					match.JSONKeyPresent(result0 + ".content"),
					match.JSONKeyPresent(result0 + ".type"),
					match.JSONKeyEqual(result0+".content.body", "hello, world"),
				},
			})
		})

		// sytest: Can get context around search results
		t.Run("Can get context around search results", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})

			for i := 1; i <= 7; i++ {
				alice.SendEventSynced(t, roomID, b.Event{
					Type: "m.room.message",
					Content: map[string]interface{}{
						"body":    fmt.Sprintf("Message number %d", i),
						"msgtype": "m.text",
					},
				})
			}

			searchRequest := client.WithJSONBody(t, map[string]interface{}{
				"search_categories": map[string]interface{}{
					"room_events": map[string]interface{}{
						"keys":        []string{"content.body"},
						"search_term": "Message 4",
						"order_by":    "recent",
						"filter": map[string]interface{}{
							"limit": 1,
							"rooms": []string{roomID},
						},
						"event_context": map[string]interface{}{
							"before_limit": 2,
							"after_limit":  2,
						},
					},
				},
			})

			resp := alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "search"}, searchRequest)
			sce := "search_categories.room_events"
			result0 := sce + ".results.0.result"
			resBefore := sce + ".results.0.context.events_before"
			resAfter := sce + ".results.0.context.events_after"
			must.MatchResponse(t, resp, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONKeyPresent(sce + ".count"),
					match.JSONKeyPresent(sce + ".results"),
					match.JSONKeyPresent(sce + ".next_batch"),
					match.JSONKeyEqual(sce+".count", float64(1)),
					match.JSONKeyEqual(result0+".room_id", roomID),
					match.JSONKeyPresent(result0 + ".content"),
					match.JSONKeyPresent(result0 + ".type"),
					match.JSONKeyEqual(result0+".content.body", "Message number 4"),
					match.JSONKeyEqual(resBefore+".0.content.body", "Message number 3"),
					match.JSONKeyEqual(resBefore+".1.content.body", "Message number 2"),
					match.JSONKeyEqual(resAfter+".0.content.body", "Message number 5"),
					match.JSONKeyEqual(resAfter+".1.content.body", "Message number 6"),
				},
			})
		})

		// sytest: Can back-paginate search results
		t.Run("Can back-paginate search results", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
			})

			eventIDs := make([]string, 20)
			for i := 0; i <= 19; i++ {
				eventID := alice.SendEventSynced(t, roomID, b.Event{
					Type: "m.room.message",
					Content: map[string]interface{}{
						"body":    fmt.Sprintf("Message number %d", i),
						"msgtype": "m.text",
					},
				})
				eventIDs[i] = eventID
			}

			searchRequest := client.WithJSONBody(t, map[string]interface{}{
				"search_categories": map[string]interface{}{
					"room_events": map[string]interface{}{
						"keys":        []string{"content.body"},
						"search_term": "Message",
						"order_by":    "recent",
						"filter": map[string]interface{}{
							"limit": 10,
							"rooms": []string{roomID},
						},
					},
				},
			})

			resp := alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "search"}, searchRequest)

			// First search result
			nextBatch := checkBackpaginateResult(t, resp, 20, eventIDs[19], eventIDs[10])

			if nextBatch == "" {
				t.Fatalf("no next batch set!")
			}

			values := url.Values{}
			values.Set("next_batch", nextBatch)
			params := client.WithQueries(values)
			resp = alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "search"}, searchRequest, params)
			// Second search result
			nextBatch = checkBackpaginateResult(t, resp, 20, eventIDs[9], eventIDs[0])

			// At this point we expect next_batch to be empty
			values.Set("next_batch", nextBatch)
			params = client.WithQueries(values)
			resp = alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "search"}, searchRequest, params)
			// third search result
			sce := "search_categories.room_events"
			result0 := sce + ".results.0.result"
			must.MatchResponse(t, resp, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONKeyPresent(sce + ".count"),
					match.JSONKeyPresent(sce + ".results"),
					match.JSONKeyEqual(sce+".count", float64(20)),
					match.JSONKeyMissing(result0),
					match.JSONKeyMissing(sce + ".next_batch"),
				},
			})
		})

		// sytest: Search works across an upgraded room and its predecessor
		t.Run("Search works across an upgraded room and its predecessor", func(t *testing.T) {
			t.Parallel()
			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset":  "private_chat",
				"version": "8",
			})

			eventBeforeUpgrade := alice.SendEventSynced(t, roomID, b.Event{
				Type: "m.room.message",
				Content: map[string]interface{}{
					"body":    "Message before upgrade",
					"msgtype": "m.text",
				},
			})
			t.Logf("Sent message before upgrade with event ID %s", eventBeforeUpgrade)

			upgradeBody := client.WithJSONBody(t, map[string]string{
				"new_version": "9",
			})
			upgradeResp := alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "upgrade"}, upgradeBody)
			body := must.ParseJSON(t, upgradeResp.Body)
			newRoomID := must.GetJSONFieldStr(t, body, "replacement_room")
			t.Logf("Replaced room %s with %s", roomID, newRoomID)

			eventAfterUpgrade := alice.SendEventSynced(t, newRoomID, b.Event{
				Type: "m.room.message",
				Content: map[string]interface{}{
					"body":    "Message after upgrade",
					"msgtype": "m.text",
				},
			})
			t.Logf("Sent message after upgrade with event ID %s", eventAfterUpgrade)

			searchRequest := client.WithJSONBody(t, map[string]interface{}{
				"search_categories": map[string]interface{}{
					"room_events": map[string]interface{}{
						"keys":        []string{"content.body"},
						"search_term": "upgrade",
						"filter": map[string]interface{}{
							"rooms": []string{roomID, newRoomID},
						},
					},
				},
			})

			resp := alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "search"}, searchRequest)
			sce := "search_categories.room_events"

			expectedEvents := map[string]string{
				eventBeforeUpgrade: "Message before upgrade",
				eventAfterUpgrade:  "Message after upgrade",
			}
			must.MatchResponse(t, resp, match.HTTPResponse{
				StatusCode: http.StatusOK,
				JSON: []match.JSON{
					match.JSONKeyEqual(sce+".count", float64(2)),
					match.JSONKeyArrayOfSize(sce+".results", 2),

					// the results can be in either order: check that both are there and that the content is as expected
					match.JSONCheckOff(sce+".results", []interface{}{eventBeforeUpgrade, eventAfterUpgrade}, func(res gjson.Result) interface{} {
						return res.Get("result.event_id").Str
					}, func(eventID interface{}, result gjson.Result) error {
						matchers := []match.JSON{
							match.JSONKeyEqual("result.type", "m.room.message"),
							match.JSONKeyEqual("result.content.body", expectedEvents[eventID.(string)]),
						}
						for _, jm := range matchers {
							if err := jm([]byte(result.Raw)); err != nil {
								return err
							}
						}
						return nil
					}),
				},
			})
		})

		for _, ordering := range []string{"rank", "recent"} {
			// sytest: Search results with $ordering_type ordering do not include redacted events
			t.Run(fmt.Sprintf("Search results with %s ordering do not include redacted events", ordering), func(t *testing.T) {
				t.Parallel()
				roomID := alice.CreateRoom(t, map[string]interface{}{
					"preset": "private_chat",
				})

				redactedEventID := alice.SendEventSynced(t, roomID, b.Event{
					Type: "m.room.message",
					Content: map[string]interface{}{
						"body":    "This message is going to be redacted",
						"msgtype": "m.text",
					},
				})

				wantContentBody := "This message is not going to be redacted"
				visibleEventID := alice.SendEventSynced(t, roomID, b.Event{
					Type: "m.room.message",
					Content: map[string]interface{}{
						"body":    wantContentBody,
						"msgtype": "m.text",
					},
				})

				// redact the event
				redactBody := client.WithJSONBody(t, map[string]interface{}{"reason": "testing"})
				txnID := util.RandomString(8) // random string, as time.Now().Unix() might create the same txnID
				resp := alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "redact", redactedEventID, txnID}, redactBody)
				j := must.ParseJSON(t, resp.Body)
				redactionEventID := must.GetJSONFieldStr(t, j, "event_id")
				// wait for the redaction to come down sync
				alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, redactionEventID))

				searchRequest := client.WithJSONBody(t, map[string]interface{}{
					"search_categories": map[string]interface{}{
						"room_events": map[string]interface{}{
							"keys":        []string{"content.body"},
							"order_by":    ordering,
							"search_term": "redacted",
							"filter": map[string]interface{}{
								"rooms": []string{roomID},
							},
						},
					},
				})

				resp = alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "search"}, searchRequest)
				sce := "search_categories.room_events"
				result0 := sce + ".results.0.result"
				must.MatchResponse(t, resp, match.HTTPResponse{
					StatusCode: http.StatusOK,
					JSON: []match.JSON{
						match.JSONKeyPresent(sce + ".count"),
						match.JSONKeyPresent(sce + ".results"),
						match.JSONKeyArrayOfSize(sce+".results", 1),
						match.JSONKeyPresent(result0 + ".content"),
						match.JSONKeyPresent(result0 + ".type"),
						match.JSONKeyEqual(result0+".event_id", visibleEventID),
						match.JSONKeyEqual(result0+".content.body", wantContentBody),
					},
				})
			})
		}

	})
}

func checkBackpaginateResult(t *testing.T, resp *http.Response, wantCount float64, lastEventID, firstEventID string) (nextBatch string) {
	sce := "search_categories.room_events"
	result0 := sce + ".results.0.result"
	result9 := sce + ".results.9.result"
	must.MatchResponse(t, resp, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONKeyPresent(sce + ".count"),
			match.JSONKeyPresent(sce + ".results"),
			match.JSONMapEach(sce, func(k, v gjson.Result) error {
				if k.Str == "next_batch" {
					nextBatch = v.Str
					return nil
				}
				return nil
			}),
			match.JSONKeyPresent(sce + ".next_batch"),
			match.JSONKeyEqual(sce+".count", wantCount),
			match.JSONKeyPresent(result0 + ".content"),
			match.JSONKeyPresent(result0 + ".type"),
			match.JSONKeyEqual(result0+".event_id", lastEventID),
			match.JSONKeyEqual(result9+".event_id", firstEventID),
		},
	})
	return
}
