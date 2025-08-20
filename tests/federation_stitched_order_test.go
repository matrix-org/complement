package tests

import (
	//"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	//"strings"
	"testing"

	//"github.com/gorilla/mux"
	"github.com/matrix-org/complement"
	"github.com/matrix-org/gomatrixserverlib"

	//"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/fclient"
	//"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/matrix-org/util"
	"github.com/tidwall/gjson"

	//"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	//"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	//"github.com/matrix-org/complement/runtime"
)

// TODO: another test for where stitched disagrees with topological order

func TestStitchedOrder(t *testing.T) {
	// 1) Create a room between the HS and Complement, with events.
	// 2) Send events with missing prev_events to HS over /send
	// 3) HS attempts to fetch missing events using /get_missing_events
	// 4) Complement gives back some but not all
	// 5) Client asks the HS to backpaginate using /messages
	// 6) HS makes a /backfill request to Complement
	// 7) Complement replies with the rest of the messages
	// 8) HS responds to client /messages request with events in the right order
	// 9) Client walking the entire room using /messages sees them in the right ordrer

	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(nil, nil),
		federation.HandleEventRequests(),
	)
	cancel := srv.Listen()
	defer cancel()

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := srv.UserID("bob")

	// 1) Create a room between the HS and Complement.
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})
	srvRoom := srv.MustJoinRoom(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), roomID, bob)
	lastSharedEvent := srvRoom.Timeline[len(srvRoom.Timeline)-1]
	roomState := srvRoom.AllCurrentState()

	// Respond correctly to state and state_ids requests
	srv.Mux().HandleFunc("/_matrix/federation/v1/state/{roomID}", func(w http.ResponseWriter, req *http.Request) {
		t.Logf("Received request to /_matrix/federation/v1/state/{roomID}")

		res := fclient.RespState{
			AuthEvents:  gomatrixserverlib.NewEventJSONsFromEvents(srvRoom.AuthChainForEvents(roomState)),
			StateEvents: gomatrixserverlib.NewEventJSONsFromEvents(roomState),
		}
		w.WriteHeader(200)
		jsonb, _ := json.Marshal(res)

		if _, err := w.Write(jsonb); err != nil {
			t.Errorf("Error writing to request: %v", err)
		}

	}).Methods("GET")
	srv.Mux().HandleFunc("/_matrix/federation/v1/state_ids/{roomID}", func(w http.ResponseWriter, req *http.Request) {

		t.Logf("Received request to /_matrix/federation/v1/state_ids/{roomID}")
		res := fclient.RespStateIDs{
			AuthEventIDs:  eventIDsFromEvents(srvRoom.AuthChainForEvents(roomState)),
			StateEventIDs: eventIDsFromEvents(roomState),
		}

		t.Logf("res=%+v", res)

		w.WriteHeader(200)
		jsonb, _ := json.Marshal(res)

		if _, err := w.Write(jsonb); err != nil {
			t.Errorf("Error writing to request: %v", err)
		}

	}).Methods("GET")

	// 2) Inject events into Complement but don't deliver them to the HS.
	var missingEvents1 []json.RawMessage
	var missingEventIDs1 []string
	//var lastMissingEvent1ID string
	numMissingEvents := 5
	for i := 0; i < numMissingEvents; i++ {
		missingEvent := srv.MustCreateEvent(t, srvRoom, federation.Event{
			Sender: bob,
			Type:   "m.room.message",
			Content: map[string]interface{}{
				"body": fmt.Sprintf("Backfilled %d/%d", i+1, numMissingEvents),
			},
		})
		srvRoom.AddEvent(missingEvent)
		missingEvents1 = append(missingEvents1, missingEvent.JSON())
		missingEventIDs1 = append(missingEventIDs1, missingEvent.EventID())
		//lastMissingEvent1ID = missingEvent.EventID()
		t.Logf("Created event 1 %s", missingEvent.EventID())
	}

	var missingEvents2 []json.RawMessage
	var missingEventIDs2 []string
	numMissingEvents = 5
	for i := 0; i < numMissingEvents; i++ {
		missingEvent := srv.MustCreateEvent(t, srvRoom, federation.Event{
			Sender: bob,
			Type:   "m.room.message",
			Content: map[string]interface{}{
				"body": fmt.Sprintf("got missing %d/%d", i+1, numMissingEvents),
			},
		})
		srvRoom.AddEvent(missingEvent)
		missingEvents2 = append(missingEvents2, missingEvent.JSON())
		missingEventIDs2 = append(missingEventIDs2, missingEvent.EventID())
		t.Logf("Created event 2 %s", missingEvent.EventID())
	}

	// 3) Inject a final event into Complement
	mostRecentEvent := srv.MustCreateEvent(t, srvRoom, federation.Event{
		Sender: bob,
		Type:   "m.room.message",
		Content: map[string]interface{}{
			"body": "most recent event",
		},
	})
	srvRoom.AddEvent(mostRecentEvent)
	t.Logf("Created event mostRecentEvent %s", mostRecentEvent.EventID())

	// 4) Respond to /get_missing_events with the missing events if the request is well-formed.
	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/get_missing_events/{roomID}",
		srv.ValidFederationRequest(t, func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
			if pathParams["roomID"] != roomID {
				t.Errorf("Received /get_missing_events for the wrong room: %s", roomID)
				return util.JSONResponse{
					Code: 400,
					JSON: "wrong room",
				}
			}
			must.MatchFederationRequest(t, fr,
				match.JSONKeyEqual("earliest_events", []interface{}{lastSharedEvent.EventID()}),
				match.JSONKeyEqual("latest_events", []interface{}{mostRecentEvent.EventID()}),
			)
			t.Logf(
				"/get_missing_events request well-formed, sending back response, earliest_events=%v latest_events=%v",
				lastSharedEvent.EventID(), mostRecentEvent.EventID(),
			)
			return util.JSONResponse{
				Code: 200,
				JSON: map[string]interface{}{
					"events": missingEvents2,
				},
			}
		}),
	).Methods("POST")

	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/backfill/{roomID}",
		srv.ValidFederationRequest(t, func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {

			// TODO: check v is correct
			//v := req.URL.Query().Get("v")
			//must.Equal(t, v, lastMissingEvent1ID, "unexpected last missing event ID")

			if pathParams["roomID"] != roomID {
				t.Errorf("Received /backfill for the wrong room: %s", roomID)
				return util.JSONResponse{
					Code: 400,
					JSON: "wrong room",
				}
			}
			t.Logf("/backfill request well-formed, sending back response")

			return util.JSONResponse{
				Code: 200,
				JSON: map[string]interface{}{
					"origin":           srv.ServerName(),
					"origin_server_ts": 35454,
					"pdus":             missingEvents1,
				},
			}
		}),
	).Methods("GET")

	// 3) ...and send that alone to the HS.
	srv.MustSendTransaction(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), []json.RawMessage{mostRecentEvent.JSON()}, nil)

	//t.Logf("Sleeping")
	//time.Sleep(1 * time.Hour)

	// 6) Ensure Alice sees all injected events in the correct order.
	correctOrderEventIDs := append([]string{lastSharedEvent.EventID()}, missingEventIDs2...)
	correctOrderEventIDs = append(correctOrderEventIDs, mostRecentEvent.EventID())
	startedGettingEvents := false
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		next := correctOrderEventIDs[0]
		if r.Get("event_id").Str == next {
			startedGettingEvents = true
			correctOrderEventIDs = correctOrderEventIDs[1:]
			if len(correctOrderEventIDs) == 0 {
				return true
			}
		} else if startedGettingEvents {
			// once we start reading the events we want we should not see any other events to ensure
			// that the order is correct
			t.Errorf("Expected timeline event %s but got %s", next, r.Get("event_id").Str)
		}
		t.Logf("Ignoring event %s", r.Get("event_id").Str)
		return false
	}))
	if len(correctOrderEventIDs) != 0 {
		t.Errorf("missed some event IDs : %v", correctOrderEventIDs)
	}

	body, _ := alice.MustSync(t, client.SyncReq{Filter: `{"room":{"timeline":{"limit":1}}}`})
	prev_batch := body.Get("rooms.join." + client.GjsonEscape(roomID) + ".timeline.prev_batch").Str

	testMessage := "JJJJJJJJJJJJJJJJJJ"

	queryParams := url.Values{}
	queryParams.Set("dir", "b")
	queryParams.Set("from", prev_batch)
	res := alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONArrayEach("chunk", func(r gjson.Result) error {
				if r.Get("type").Str == "m.room.message" && r.Get("content").Get("body").Str == testMessage {
					return nil
				}
				return fmt.Errorf("did not find correct event")
			}),
		},
	})

	//t.Logf("Sleeping")
	//time.Sleep(1 * time.Hour)
	//t.Logf("Finished sleeping")
}

func eventIDsFromEvents(he []gomatrixserverlib.PDU) []string {
	eventIDs := make([]string, len(he))
	for i := range he {
		eventIDs[i] = he[i].EventID()
	}
	return eventIDs
}
