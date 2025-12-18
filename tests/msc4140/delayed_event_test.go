package tests

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
	"github.com/matrix-org/complement/should"
	"github.com/tidwall/gjson"
)

const hsName = "hs1"
const eventType = "com.example.test"

type DelayedEventAction string

const (
	DelayedEventActionCancel  = "cancel"
	DelayedEventActionRestart = "restart"
	DelayedEventActionSend    = "send"
)

// TODO: Test pagination of `GET /_matrix/client/v1/delayed_events` once
// it is implemented in a homeserver.

func TestDelayedEvents(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	user := deployment.Register(t, hsName, helpers.RegistrationOpts{})
	user2 := deployment.Register(t, hsName, helpers.RegistrationOpts{})
	unauthedClient := deployment.UnauthenticatedClient(t, hsName)

	roomID := user.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
		"power_level_content_override": map[string]interface{}{
			"events": map[string]int{
				eventType: 0,
			},
		},
	})
	user2.MustJoinRoom(t, roomID, nil)

	t.Run("delayed events are empty on startup", func(t *testing.T) {
		matchDelayedEvents(t, user, delayedEventsNumberEqual(0))
	})

	t.Run("delayed event lookups are authenticated", func(t *testing.T) {
		res := unauthedClient.Do(t, "GET", getPathForDelayedEvents())
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 401,
		})
	})

	t.Run("delayed message events are sent on timeout", func(t *testing.T) {
		var res *http.Response
		var countExpected uint64

		_, token := user.MustSync(t, client.SyncReq{})

		defer cleanupDelayedEvents(t, user)

		txnIdBase := "txn-delayed-msg-timeout-%d"

		countKey := "count"
		numEvents := 3
		for i, delayStr := range []string{"700", "800", "900"} {
			res = user.MustDo(
				t,
				"PUT",
				getPathForSend(roomID, eventType, fmt.Sprintf(txnIdBase, i)),
				client.WithJSONBody(t, map[string]interface{}{
					countKey: i + 1,
				}),
				getDelayQueryParam(delayStr),
			)
			delayID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "delay_id")

			t.Run("rerequesting delayed event path with the same txnID should have the same response", func(t *testing.T) {
				res := user.MustDo(
					t,
					"PUT",
					getPathForSend(roomID, eventType, fmt.Sprintf(txnIdBase, i)),
					getDelayQueryParam(delayStr),
				)
				must.MatchResponse(t, res, match.HTTPResponse{
					JSON: []match.JSON{
						match.JSONKeyEqual("delay_id", delayID),
					},
				})
			})
		}

		countExpected = 0
		matchDelayedEvents(t, user, delayedEventsNumberEqual(numEvents))

		t.Run("cannot get delayed events of another user", func(t *testing.T) {
			matchDelayedEvents(t, user2, delayedEventsNumberEqual(0))
		})

		time.Sleep(1 * time.Second)
		matchDelayedEvents(t, user, delayedEventsNumberEqual(0))
		queryParams := url.Values{}
		queryParams.Set("dir", "f")
		queryParams.Set("from", token)
		res = user.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
		countExpected = 0
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONArrayEach("chunk", func(val gjson.Result) error {
					content := val.Get("content").Map()
					if l := len(content); l != 1 {
						return fmt.Errorf("wrong number of content fields: expected 1, got %d", l)
					}
					countExpected++
					if countActual := content[countKey].Uint(); countActual != countExpected {
						return fmt.Errorf("wrong count in delayed event content: expected %v, got %v", countExpected, countActual)
					}
					return nil
				}),
			},
		})
	})

	t.Run("delayed state events are sent on timeout", func(t *testing.T) {
		var res *http.Response

		defer cleanupDelayedEvents(t, user)

		stateKey := "to_send_on_timeout"

		setterKey := "setter"
		setterExpected := "on_timeout"
		user.MustDo(
			t,
			"PUT",
			getPathForState(roomID, eventType, stateKey),
			client.WithJSONBody(t, map[string]interface{}{
				setterKey: setterExpected,
			}),
			getDelayQueryParam("900"),
		)

		matchDelayedEvents(t, user, delayedEventsNumberEqual(1))

		res = getDelayedEvents(t, user)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONArrayEach("delayed_events", func(val gjson.Result) error {
					content := val.Get("content").Map()
					if l := len(content); l != 1 {
						return fmt.Errorf("wrong number of content fields: expected 1, got %d", l)
					}
					if setterActual := content[setterKey].Str; setterActual != setterExpected {
						return fmt.Errorf("wrong setter in delayed event content: expected %v, got %v", setterExpected, setterActual)
					}
					return nil
				}),
			},
		})
		res = user.Do(t, "GET", getPathForState(roomID, eventType, stateKey))
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
		})

		time.Sleep(1 * time.Second)
		matchDelayedEvents(t, user, delayedEventsNumberEqual(0))
		res = user.MustDo(t, "GET", getPathForState(roomID, eventType, stateKey))
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual(setterKey, setterExpected),
			},
		})
	})

	t.Run("cannot update a delayed event without an action", func(t *testing.T) {
		res := unauthedClient.Do(
			t,
			"POST",
			append(getPathForDelayedEvents(), "abc"),
			client.WithJSONBody(t, map[string]interface{}{}),
		)
		// TODO: specify failure as 404 when/if Synapse removes the action-in-body version of this endpoint
		must.MatchFailure(t, res)
	})

	t.Run("cannot update a delayed event with an invalid action", func(t *testing.T) {
		res := unauthedClient.Do(
			t,
			"POST",
			append(getPathForDelayedEvents(), "abc", "oops"),
			client.WithJSONBody(t, map[string]interface{}{}),
		)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
		})
	})

	t.Run("parallel", func(t *testing.T) {
		for _, action := range []DelayedEventAction{
			DelayedEventActionCancel,
			DelayedEventActionRestart,
			DelayedEventActionSend,
		} {
			t.Run(fmt.Sprintf("cannot %s a delayed event without a matching delay ID", action), func(t *testing.T) {
				t.Parallel()
				res := unauthedClient.Do(
					t,
					"POST",
					getPathForUpdateDelayedEvent("abc", action),
					client.WithJSONBody(t, map[string]interface{}{}),
				)
				must.MatchResponse(t, res, match.HTTPResponse{
					StatusCode: 404,
				})
			})
		}
	})

	t.Run("delayed state events can be cancelled", func(t *testing.T) {
		var res *http.Response

		stateKey := "to_never_send"

		setterKey := "setter"
		setterExpected := "none"
		res = user.MustDo(
			t,
			"PUT",
			getPathForState(roomID, eventType, stateKey),
			client.WithJSONBody(t, map[string]interface{}{
				setterKey: setterExpected,
			}),
			getDelayQueryParam("1500"),
		)
		delayID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "delay_id")

		time.Sleep(1 * time.Second)
		matchDelayedEvents(t, user, delayedEventsNumberEqual(1))
		res = user.Do(t, "GET", getPathForState(roomID, eventType, stateKey))
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
		})

		unauthedClient.MustDo(
			t,
			"POST",
			getPathForUpdateDelayedEvent(delayID, DelayedEventActionCancel),
			client.WithJSONBody(t, map[string]interface{}{}),
		)
		matchDelayedEvents(t, user, delayedEventsNumberEqual(0))

		time.Sleep(1 * time.Second)
		res = user.Do(t, "GET", getPathForState(roomID, eventType, stateKey))
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
		})
	})

	t.Run("delayed state events can be sent on request", func(t *testing.T) {
		var res *http.Response

		defer cleanupDelayedEvents(t, user)

		stateKey := "to_send_on_request"

		setterKey := "setter"
		setterExpected := "on_send"
		res = user.MustDo(
			t,
			"PUT",
			getPathForState(roomID, eventType, stateKey),
			client.WithJSONBody(t, map[string]interface{}{
				setterKey: setterExpected,
			}),
			getDelayQueryParam("100000"),
		)
		delayID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "delay_id")

		time.Sleep(1 * time.Second)
		matchDelayedEvents(t, user, delayedEventsNumberEqual(1))
		res = user.Do(t, "GET", getPathForState(roomID, eventType, stateKey))
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
		})

		unauthedClient.MustDo(
			t,
			"POST",
			getPathForUpdateDelayedEvent(delayID, DelayedEventActionSend),
			client.WithJSONBody(t, map[string]interface{}{}),
		)
		matchDelayedEvents(t, user, delayedEventsNumberEqual(0))
		res = user.Do(t, "GET", getPathForState(roomID, eventType, stateKey))
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual(setterKey, setterExpected),
			},
		})
	})

	t.Run("delayed state events can be restarted", func(t *testing.T) {
		var res *http.Response

		stateKey := "to_send_on_restarted_timeout"

		defer cleanupDelayedEvents(t, user)

		setterKey := "setter"
		setterExpected := "on_timeout"
		res = user.MustDo(
			t,
			"PUT",
			getPathForState(roomID, eventType, stateKey),
			client.WithJSONBody(t, map[string]interface{}{
				setterKey: setterExpected,
			}),
			getDelayQueryParam("1500"),
		)
		delayID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "delay_id")

		time.Sleep(1 * time.Second)
		matchDelayedEvents(t, user, delayedEventsNumberEqual(1))
		res = user.Do(t, "GET", getPathForState(roomID, eventType, stateKey))
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
		})

		unauthedClient.MustDo(
			t,
			"POST",
			getPathForUpdateDelayedEvent(delayID, DelayedEventActionRestart),
			client.WithJSONBody(t, map[string]interface{}{}),
		)

		time.Sleep(1 * time.Second)
		matchDelayedEvents(t, user, delayedEventsNumberEqual(1))
		res = user.Do(t, "GET", getPathForState(roomID, eventType, stateKey))
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
		})

		time.Sleep(1 * time.Second)
		matchDelayedEvents(t, user, delayedEventsNumberEqual(0))
		res = user.MustDo(t, "GET", getPathForState(roomID, eventType, stateKey))
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual(setterKey, setterExpected),
			},
		})
	})

	t.Run("delayed state is not cancelled by new state from the same user", func(t *testing.T) {
		var res *http.Response

		stateKey := "to_not_be_cancelled_by_same_user"

		defer cleanupDelayedEvents(t, user)

		setterKey := "setter"
		setterExpected := "on_timeout"
		user.MustDo(
			t,
			"PUT",
			getPathForState(roomID, eventType, stateKey),
			client.WithJSONBody(t, map[string]interface{}{
				setterKey: setterExpected,
			}),
			getDelayQueryParam("900"),
		)
		matchDelayedEvents(t, user, delayedEventsNumberEqual(1))

		user.MustDo(
			t,
			"PUT",
			getPathForState(roomID, eventType, stateKey),
			client.WithJSONBody(t, map[string]interface{}{
				setterKey: "manual",
			}),
		)
		matchDelayedEvents(t, user, delayedEventsNumberEqual(1))

		time.Sleep(1 * time.Second)
		res = user.MustDo(t, "GET", getPathForState(roomID, eventType, stateKey))
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual(setterKey, setterExpected),
			},
		})
	})

	t.Run("delayed state is cancelled by new state from another user", func(t *testing.T) {
		var res *http.Response

		stateKey := "to_be_cancelled_by_other_user"

		defer cleanupDelayedEvents(t, user)
		defer cleanupDelayedEvents(t, user2)

		setterKey := "setter"
		user.MustDo(
			t,
			"PUT",
			getPathForState(roomID, eventType, stateKey),
			client.WithJSONBody(t, map[string]interface{}{
				setterKey: "on_timeout",
			}),
			getDelayQueryParam("900"),
		)
		matchDelayedEvents(t, user, delayedEventsNumberEqual(1))

		setterExpected := "manual"
		user2.MustDo(
			t,
			"PUT",
			getPathForState(roomID, eventType, stateKey),
			client.WithJSONBody(t, map[string]interface{}{
				setterKey: setterExpected,
			}),
		)
		matchDelayedEvents(t, user, delayedEventsNumberEqual(0))

		time.Sleep(1 * time.Second)
		res = user.MustDo(t, "GET", getPathForState(roomID, eventType, stateKey))
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual(setterKey, setterExpected),
			},
		})
	})

	t.Run("delayed state events are kept on server restart", func(t *testing.T) {
		// Spec cannot enforce server restart behaviour
		runtime.SkipIf(t, runtime.Dendrite, runtime.Conduit, runtime.Conduwuit)

		defer cleanupDelayedEvents(t, user)

		stateKey1 := "1"
		stateKey2 := "2"

		numberOfDelayedEvents := 0

		// Send an initial delayed event that will be ready to send as soon as the server
		// comes back up.
		user.MustDo(
			t,
			"PUT",
			getPathForState(roomID, eventType, stateKey1),
			client.WithJSONBody(t, map[string]interface{}{}),
			getDelayQueryParam("900"),
		)
		numberOfDelayedEvents++

		// Previously, this was naively using a single delayed event with a 10 second delay.
		// But because we're stopping and starting servers here, it could take up
		// `deployment.GetConfig().SpawnHSTimeout` (defaults to 30 seconds) for the server
		// to start up again so by the time the server is back up, the delayed event may
		// have already been sent invalidating our assertions below (which expect some
		// delayed events to still be pending and then see one of them be sent after the
		// server is back up).
		//
		// We could account for this by setting the delayed event delay to be longer than
		// `deployment.GetConfig().SpawnHSTimeout` but that would make the test suite take
		// longer to run in all cases even for homeservers that are quick to restart because
		// we have to wait for that large delay.
		//
		// We instead account for this by scheduling many delayed events at short intervals
		// (we chose 10 seconds because that's what the test naively chose before). Then
		// whenever the servers comes back, we can just check until it decrements by 1.
		//
		// We add 1 to the number of intervals to ensure that we have at least one interval
		// to check against no matter how things are configured.
		numberOf10SecondIntervals := int(math.Ceil(deployment.GetConfig().SpawnHSTimeout.Seconds()/10)) + 1
		for i := 0; i < numberOf10SecondIntervals; i++ {
			// +1 as we want to start at 10 seconds and so we don't end up with -100ms delay
			// on the first one.
			delay := time.Duration(i+1)*10*time.Second - 100*time.Millisecond

			user.MustDo(
				t,
				"PUT",
				// Avoid clashing state keys as that would cancel previous delayed events on the
				// same key (start at 2).
				getPathForState(roomID, eventType, fmt.Sprintf("%d", i+2)),
				client.WithJSONBody(t, map[string]interface{}{}),
				getDelayQueryParam(fmt.Sprintf("%d", delay.Milliseconds())),
			)
			numberOfDelayedEvents++
		}
		// We expect all of the delayed events to be scheduled and not sent yet.
		matchDelayedEvents(t, user, delayedEventsNumberEqual(numberOfDelayedEvents))

		// Restart the server and wait until it's back up.
		deployment.StopServer(t, hsName)
		// Wait one second which will cause the first delayed event to be ready to be sent
		// when the server is back up.
		time.Sleep(1 * time.Second)
		deployment.StartServer(t, hsName)

		delayedEventResponse := matchDelayedEvents(t, user,
			// We should still see some delayed events left after the restart.
			delayedEventsNumberGreaterThan(0),
			// We should see at-least one less than we had before the restart (the first
			// delayed event should have been sent). Other delayed events may have been sent
			// by the time the server actually came back up.
			delayedEventsNumberLessThan(numberOfDelayedEvents-1),
		)
		// Capture whatever number of delayed events are remaining after the server restart.
		remainingDelayedEventCount := countDelayedEvents(t, delayedEventResponse)
		// Sanity check that the room state was updated correctly with the delayed events
		// that were sent.
		user.MustDo(t, "GET", getPathForState(roomID, eventType, stateKey1))

		// Wait until we see another delayed event being sent (ensure things resumed and are continuing).
		time.Sleep(10 * time.Second)
		matchDelayedEvents(t, user,
			delayedEventsNumberLessThan(remainingDelayedEventCount),
		)
		// Sanity check that the other delayed events also updated the room state correctly.
		//
		// FIXME: Ideally, we'd check specifically for the last one that was sent but it
		// will be a bit of a juggle and fiddly to get this right so for now we just check
		// one.
		user.MustDo(t, "GET", getPathForState(roomID, eventType, stateKey2))
	})
}

func getPathForDelayedEvents() []string {
	return []string{"_matrix", "client", "unstable", "org.matrix.msc4140", "delayed_events"}
}

func getPathForUpdateDelayedEvent(delayId string, action DelayedEventAction) []string {
	return append(getPathForDelayedEvents(), delayId, string(action))
}

func getPathForSend(roomID string, eventType string, txnId string) []string {
	return []string{"_matrix", "client", "v3", "rooms", roomID, "send", eventType, txnId}
}

func getPathForState(roomID string, eventType string, stateKey string) []string {
	return []string{"_matrix", "client", "v3", "rooms", roomID, "state", eventType, stateKey}
}

func getDelayQueryParam(delayStr string) client.RequestOpt {
	return client.WithQueries(url.Values{
		"org.matrix.msc4140.delay": []string{delayStr},
	})
}

func getDelayedEvents(t *testing.T, user *client.CSAPI) *http.Response {
	t.Helper()
	return user.MustDo(t, "GET", getPathForDelayedEvents())
}

// countDelayedEvents counts the number of delayed events in the response. Assumes the
// response is well-formed.
func countDelayedEventsInternal(res *http.Response) (int, error) {
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, fmt.Errorf("countDelayedEventsInternal: Failed to read response body: %s", err)
	}

	parsedBody := gjson.ParseBytes(body)
	return len(parsedBody.Get("delayed_events").Array()), nil
}

func countDelayedEvents(t *testing.T, res *http.Response) int {
	t.Helper()
	count, err := countDelayedEventsInternal(res)
	if err != nil {
		t.Fatalf("countDelayedEvents: %s", err)
	}
	return count
}

type delayedEventsCheckOpt func(res *http.Response) error

// delayedEventsNumberEqual returns a check option that checks if the number of delayed events
// is equal to the given number.
func delayedEventsNumberEqual(wantNumber int) delayedEventsCheckOpt {
	return func(res *http.Response) error {
		_, err := should.MatchResponse(res, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", wantNumber),
			},
		})
		if err == nil {
			return nil
		}
		return fmt.Errorf("delayedEventsNumberEqual(%d): %s", wantNumber, err)
	}
}

// delayedEventsNumberLessThan returns a check option that checks if the number of delayed events
// is greater than the given number.
func delayedEventsNumberGreaterThan(target int) delayedEventsCheckOpt {
	return func(res *http.Response) error {
		count, err := countDelayedEventsInternal(res)
		if err != nil {
			return fmt.Errorf("delayedEventsNumberGreaterThan(%d): %s", target, err)
		}
		if count > target {
			return nil
		}
		return fmt.Errorf("delayedEventsNumberGreaterThan(%d): got %d", target, count)
	}
}

// delayedEventsNumberLessThan returns a check option that checks if the number of delayed events
// is less than the given number.
func delayedEventsNumberLessThan(target int) delayedEventsCheckOpt {
	return func(res *http.Response) error {
		count, err := countDelayedEventsInternal(res)
		if err != nil {
			return fmt.Errorf("delayedEventsNumberLessThan(%d): %s", target, err)
		}
		if count < target {
			return nil
		}
		return fmt.Errorf("delayedEventsNumberLessThan(%d): got %d", target, count)
	}
}

// matchDelayedEvents will run the given checks on the delayed events response. This will
// retry to handle replication lag.
func matchDelayedEvents(t *testing.T, user *client.CSAPI, checks ...delayedEventsCheckOpt) *http.Response {
	t.Helper()

	// We need to retry this as replication can sometimes lag.
	return user.MustDo(t, "GET", getPathForDelayedEvents(),
		client.WithRetryUntil(
			500*time.Millisecond,
			func(res *http.Response) bool {
				for _, check := range checks {
					err := check(res)

					if err != nil {
						t.Log(err)
						return false
					}
				}
				return true
			},
		),
	)
}

func cleanupDelayedEvents(t *testing.T, user *client.CSAPI) {
	t.Helper()
	res := getDelayedEvents(t, user)
	defer res.Body.Close()
	body := must.ParseJSON(t, res.Body)
	for _, delayedEvent := range body.Get("delayed_events").Array() {
		delayID := delayedEvent.Get("delay_id").String()
		user.MustDo(
			t,
			"POST",
			getPathForUpdateDelayedEvent(delayID, DelayedEventActionCancel),
			client.WithJSONBody(t, map[string]interface{}{}),
		)
	}

	matchDelayedEvents(t, user, delayedEventsNumberEqual(0))
}
