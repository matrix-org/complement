package tests

import (
	"fmt"
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
	"github.com/tidwall/gjson"
)

const hsName = "hs1"
const eventType = "com.example.test"

// TODO: Test pagination of `GET /_matrix/client/v1/delayed_events` once
// it is implemented in a homeserver.

func TestDelayedEvents(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	user := deployment.Register(t, hsName, helpers.RegistrationOpts{})
	user2 := deployment.Register(t, hsName, helpers.RegistrationOpts{})

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
		res := getDelayedEvents(t, user)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", 0),
			},
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

		res = getDelayedEvents(t, user)
		countExpected = 0
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", numEvents),
			},
		})

		t.Run("cannot get delayed events of another user", func(t *testing.T) {
			res := getDelayedEvents(t, user2)
			must.MatchResponse(t, res, match.HTTPResponse{
				JSON: []match.JSON{
					match.JSONKeyArrayOfSize("delayed_events", 0),
				},
			})
		})

		time.Sleep(1 * time.Second)
		res = getDelayedEvents(t, user)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", 0),
			},
		})
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
		res = getDelayedEvents(t, user)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", 1),
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
		res = getDelayedEvents(t, user)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", 0),
			},
		})
		res = user.MustDo(t, "GET", getPathForState(roomID, eventType, stateKey))
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual(setterKey, setterExpected),
			},
		})
	})

	t.Run("cannot update a delayed event without a delay ID", func(t *testing.T) {
		res := user.Do(t, "POST", append(getPathForUpdateDelayedEvents(), ""))
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
		})
	})

	t.Run("cannot update a delayed event without a request body", func(t *testing.T) {
		res := user.Do(t, "POST", append(getPathForUpdateDelayedEvents(), "abc"))
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 400,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_NOT_JSON"),
			},
		})
	})

	t.Run("cannot update a delayed event without an action", func(t *testing.T) {
		res := user.Do(
			t,
			"POST",
			append(getPathForUpdateDelayedEvents(), "abc"),
			client.WithJSONBody(t, map[string]interface{}{}),
		)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 400,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_MISSING_PARAM"),
			},
		})
	})

	t.Run("cannot update a delayed event with an invalid action", func(t *testing.T) {
		res := user.Do(
			t,
			"POST",
			append(getPathForUpdateDelayedEvents(), "abc"),
			client.WithJSONBody(t, map[string]interface{}{
				"action": "oops",
			}),
		)
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 400,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_INVALID_PARAM"),
			},
		})
	})

	t.Run("parallel", func(t *testing.T) {
		for _, action := range []string{"cancel", "restart", "send"} {
			t.Run(fmt.Sprintf("cannot %s a delayed event without a matching delay ID", action), func(t *testing.T) {
				t.Parallel()
				res := user.Do(
					t,
					"POST",
					append(getPathForUpdateDelayedEvents(), "abc"),
					client.WithJSONBody(t, map[string]interface{}{
						"action": action,
					}),
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
		res = getDelayedEvents(t, user)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", 1),
			},
		})
		res = user.Do(t, "GET", getPathForState(roomID, eventType, stateKey))
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
		})

		user.MustDo(
			t,
			"POST",
			append(getPathForUpdateDelayedEvents(), delayID),
			client.WithJSONBody(t, map[string]interface{}{
				"action": "cancel",
			}),
		)
		res = getDelayedEvents(t, user)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", 0),
			},
		})

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
		res = getDelayedEvents(t, user)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", 1),
			},
		})
		res = user.Do(t, "GET", getPathForState(roomID, eventType, stateKey))
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
		})

		user.MustDo(
			t,
			"POST",
			append(getPathForUpdateDelayedEvents(), delayID),
			client.WithJSONBody(t, map[string]interface{}{
				"action": "send",
			}),
		)
		res = getDelayedEvents(t, user)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", 0),
			},
		})
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
		res = getDelayedEvents(t, user)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", 1),
			},
		})
		res = user.Do(t, "GET", getPathForState(roomID, eventType, stateKey))
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
		})

		user.MustDo(
			t,
			"POST",
			append(getPathForUpdateDelayedEvents(), delayID),
			client.WithJSONBody(t, map[string]interface{}{
				"action": "restart",
			}),
		)

		time.Sleep(1 * time.Second)
		res = getDelayedEvents(t, user)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", 1),
			},
		})
		res = user.Do(t, "GET", getPathForState(roomID, eventType, stateKey))
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: 404,
		})

		time.Sleep(1 * time.Second)
		res = getDelayedEvents(t, user)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", 0),
			},
		})
		res = user.MustDo(t, "GET", getPathForState(roomID, eventType, stateKey))
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual(setterKey, setterExpected),
			},
		})
	})

	t.Run("delayed state events are cancelled by a more recent state event from the same user", func(t *testing.T) {
		var res *http.Response

		stateKey := "to_be_cancelled_by_same_user"

		defer cleanupDelayedEvents(t, user)

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
		res = getDelayedEvents(t, user)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", 1),
			},
		})

		setterExpected := "manual"
		user.MustDo(
			t,
			"PUT",
			getPathForState(roomID, eventType, stateKey),
			client.WithJSONBody(t, map[string]interface{}{
				setterKey: setterExpected,
			}),
		)
		res = getDelayedEvents(t, user)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", 0),
			},
		})

		time.Sleep(1 * time.Second)
		res = user.MustDo(t, "GET", getPathForState(roomID, eventType, stateKey))
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual(setterKey, setterExpected),
			},
		})
	})

	t.Run("delayed state events are cancelled by a more recent state event from another user", func(t *testing.T) {
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
		res = getDelayedEvents(t, user)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", 1),
			},
		})

		setterExpected := "manual"
		user2.MustDo(
			t,
			"PUT",
			getPathForState(roomID, eventType, stateKey),
			client.WithJSONBody(t, map[string]interface{}{
				setterKey: setterExpected,
			}),
		)
		res = getDelayedEvents(t, user)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", 0),
			},
		})

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

		var res *http.Response

		defer cleanupDelayedEvents(t, user)

		stateKey1 := "1"
		stateKey2 := "2"

		user.MustDo(
			t,
			"PUT",
			getPathForState(roomID, eventType, stateKey1),
			client.WithJSONBody(t, map[string]interface{}{}),
			getDelayQueryParam("900"),
		)
		user.MustDo(
			t,
			"PUT",
			getPathForState(roomID, eventType, stateKey2),
			client.WithJSONBody(t, map[string]interface{}{}),
			getDelayQueryParam("9900"),
		)
		res = getDelayedEvents(t, user)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", 2),
			},
		})

		deployment.StopServer(t, hsName)
		time.Sleep(1 * time.Second)
		deployment.StartServer(t, hsName)

		res = getDelayedEvents(t, user)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", 1),
			},
		})
		user.MustDo(t, "GET", getPathForState(roomID, eventType, stateKey1))

		time.Sleep(9 * time.Second)
		res = getDelayedEvents(t, user)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyArrayOfSize("delayed_events", 0),
			},
		})
		user.MustDo(t, "GET", getPathForState(roomID, eventType, stateKey2))
	})
}

func getPathForUpdateDelayedEvents() []string {
	return []string{"_matrix", "client", "unstable", "org.matrix.msc4140", "delayed_events"}
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
	return user.MustDo(t, "GET", getPathForUpdateDelayedEvents())
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
			append(getPathForUpdateDelayedEvents(), delayID),
			client.WithJSONBody(t, map[string]interface{}{
				"action": "cancel",
			}),
		)
	}

	must.MatchResponse(t, getDelayedEvents(t, user), match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyArrayOfSize("delayed_events", 0),
		},
	})
}
