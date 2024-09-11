package tests

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

func TestDelayedEvents(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	user := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := user.MustCreateRoom(t, map[string]interface{}{
		"preset": "trusted_private_chat",
	})

	t.Run("delayed state events are cancelled by a more recent state event", func(t *testing.T) {
		var res *http.Response

		eventType := "com.example.test"
		stateKey := "to_be_cancelled"

		user.MustDo(
			t,
			"PUT",
			getPathForState(roomID, eventType, stateKey),
			client.WithJSONBody(t, map[string]interface{}{
				"setter": "on_timeout",
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
				"setter": setterExpected,
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
				match.JSONKeyEqual("setter", setterExpected),
			},
		})
	})
}

func getPathForSend(roomID string, eventType string) []string {
	return []string{"_matrix", "client", "v3", "rooms", roomID, "send", eventType}
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
	return user.MustDo(t, "GET", []string{"_matrix", "client", "unstable", "org.matrix.msc4140", "delayed_events"})
}
