package csapi_tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/matrix-org/util"
)

// sytest: POST /rooms/:room_id/send/:event_type sends a message
// sytest: GET /rooms/:room_id/messages returns a message
func TestSendAndFetchMessage(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // flakey
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})

	const testMessage = "TestSendAndFetchMessage"

	_, token := alice.MustSync(t, client.SyncReq{})

	// first use the non-txn endpoint
	alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message"}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    testMessage,
	}))

	// sync until the server has processed it
	alice.MustSyncUntil(t, client.SyncReq{Since: token}, client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		return r.Get("type").Str == "m.room.message" && r.Get("content").Get("body").Str == testMessage
	}))

	// then request messages from the room
	queryParams := url.Values{}
	queryParams.Set("dir", "f")
	queryParams.Set("from", token)
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
}

// With a non-existent room_id, GET /rooms/:room_id/messages returns 403
// forbidden ("You aren't a member of the room").
func TestFetchMessagesFromNonExistentRoom(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	roomID := "!does-not-exist:hs1"

	// then request messages from the room
	queryParams := url.Values{}
	queryParams.Set("dir", "b")
	res := alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusForbidden,
	})
}

// sytest: PUT /rooms/:room_id/send/:event_type/:txn_id sends a message
// sytest: PUT /rooms/:room_id/send/:event_type/:txn_id deduplicates the same txn id
func TestSendMessageWithTxn(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{})

	const txnID = "lorem"

	res := alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", txnID}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "test",
	}))
	eventID := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	res = alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", txnID}, client.WithJSONBody(t, map[string]interface{}{
		"msgtype": "m.text",
		"body":    "test",
	}))
	eventID2 := client.GetJSONFieldStr(t, client.ParseJSON(t, res), "event_id")

	if eventID != eventID2 {
		t.Fatalf("two /send requests with same txn ID resulted in different event IDs")
	}
}

func TestRoomMessagesLazyLoading(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	charlie := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat"})
	bob.MustJoinRoom(t, roomID, nil)
	charlie.MustJoinRoom(t, roomID, nil)

	bob.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "test",
		},
	})

	beforeToken := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID), client.SyncJoinedTo(charlie.UserID, roomID))

	eventID := charlie.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "test",
		},
	})

	afterToken := alice.MustSyncUntil(t, client.SyncReq{Since: beforeToken}, client.SyncTimelineHas(roomID, func(r gjson.Result) bool {
		return r.Get("event_id").Str == eventID
	}))

	queryParams := url.Values{}
	queryParams.Set("dir", "f")
	queryParams.Set("filter", `{ "lazy_load_members" : true }`)
	queryParams.Set("from", beforeToken)
	queryParams.Set("to", afterToken)
	res := alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONKeyArrayOfSize("state", 1),
			match.JSONArrayEach("state", func(j gjson.Result) error {
				if j.Get("type").Str != "m.room.member" {
					return fmt.Errorf("state event is not m.room.member")
				}
				if j.Get("state_key").Str != charlie.UserID {
					return fmt.Errorf("m.room.member event is not for charlie")
				}
				if j.Get("content").Get("membership").Str != "join" {
					return fmt.Errorf("m.room.member event is not 'join'")
				}
				return nil
			}),
		},
	})

}

// TODO We should probably see if this should be removed.
//
//	Sytest tests for a very specific bug; check if local user member event loads properly when going backwards from a prev_event.
//	However, note that the sytest *only* checks for the *local, single* user in a room to be included in `/messages`.
//	This function exists here for sytest exhaustiveness, but I question its usefulness, thats why TestRoomMessagesLazyLoading
//	exists to do a more generic check.
//	We should probably see if this should be removed.
//
// sytest: GET /rooms/:room_id/messages lazy loads members correctly
func TestRoomMessagesLazyLoadingLocalUser(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{})

	token := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

	alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "test",
		},
	})

	queryParams := url.Values{}
	queryParams.Set("dir", "b")
	queryParams.Set("filter", `{ "lazy_load_members" : true }`)
	queryParams.Set("from", token)
	res := alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: http.StatusOK,
		JSON: []match.JSON{
			match.JSONKeyArrayOfSize("state", 1),
			match.JSONArrayEach("state", func(j gjson.Result) error {
				if j.Get("type").Str != "m.room.member" {
					return fmt.Errorf("state event is not m.room.member")
				}
				if j.Get("state_key").Str != alice.UserID {
					return fmt.Errorf("m.room.member event is not for alice")
				}
				if j.Get("content").Get("membership").Str != "join" {
					return fmt.Errorf("m.room.member event is not 'join'")
				}
				return nil
			}),
		},
	})
}

type MessageDraft struct {
	Sender         string
	ShareInitially bool
	Message        string
}

type EventInfo struct {
	MessageDraft MessageDraft
	PDU          gomatrixserverlib.PDU
}

func TestRoomMessagesGaps(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	// Create a remote homeserver
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		// The other server might try to send us some junk, just ignore it
		federation.HandleTransactionRequests(nil, nil),
	)
	cancel := srv.Listen()
	defer cancel()

	roomVersion := alice.GetDefaultRoomVersion(t)
	charlie := srv.UserID("charlie")
	remoteRoom := srv.MustMakeRoom(t, roomVersion, federation.InitialRoomEvents(roomVersion, charlie))

	messageDrafts := []MessageDraft{
		MessageDraft{charlie, true, "foo"},
		MessageDraft{charlie, true, "bar"},
		MessageDraft{charlie, true, "baz"},
		MessageDraft{charlie, false, "qux"},
		MessageDraft{charlie, true, "corge"},
		MessageDraft{charlie, true, "grault"},
		MessageDraft{charlie, true, "garply"},
		MessageDraft{charlie, true, "waldo"},
		MessageDraft{charlie, true, "fred"},
	}

	// Create some events
	// Map from event_id to event info
	eventIDs := make([]string, len(messageDrafts))
	eventMap := make(map[string]EventInfo)
	for messageDraftIndex, messageDraft := range messageDrafts {
		federation_event := federation.Event{
			Sender: messageDraft.Sender,
			Type:   "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    messageDraft.Message,
			},
		}
		if messageDraftIndex > 2 {
			federation_event.PrevEvents = []string{
				eventIDs[messageDraftIndex-1],
				// Always connect it to some known part of the DAG (for the local server's sake
				// later)
				eventIDs[messageDraftIndex-2],
			}
		}

		event := srv.MustCreateEvent(t, remoteRoom, federation_event)
		eventIDs[messageDraftIndex] = event.EventID()
		eventMap[event.EventID()] = EventInfo{
			MessageDraft: messageDraft,
			PDU:          event,
		}
		remoteRoom.AddEvent(event)
	}

	// Sanity check we sent all of the events in the room
	if len(eventMap) != len(messageDrafts) {
		t.Fatalf(
			"expected the number of events (%d) to match the number of message drafts we expected to send (%d)",
			len(messageDrafts),
			len(eventMap),
		)
	}

	// Make it easy to cross-reference the events being talked about in the logs
	for eventIndex, eventID := range eventIDs {
		messageDraft := eventMap[eventID].MessageDraft
		event := eventMap[eventID].PDU
		t.Logf("Message %d: %s-6s -> event_id=%s", eventIndex, messageDraft.Message, event.EventID())
	}

	// The other server is bound to ask about the missing events we reference in the
	// prev_event_ids of others but we don't divulge that to them because we want the gaps
	// to remain.
	//
	// We need to respond successfully (200 OK) so we remain on the good list of
	// federation destinations.
	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/get_missing_events/{roomID}",
		srv.ValidFederationRequest(t, func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
			t.Logf("Got /get_missing_events for %s", pathParams["roomID"])
			if pathParams["roomID"] != remoteRoom.RoomID {
				t.Errorf("Received /get_missing_events for the wrong room: %s", remoteRoom.RoomID)
				return util.JSONResponse{
					Code: 400,
					JSON: "wrong room",
				}
			}

			return util.JSONResponse{
				Code: 200,
				JSON: map[string]interface{}{
					"events": []string{},
				},
			}
		}),
	).Methods("POST")

	// TODO
	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/backfill/{roomID}",
		srv.ValidFederationRequest(t, func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
			t.Logf("Got /backfill for %s", pathParams["roomID"])
			if pathParams["roomID"] != remoteRoom.RoomID {
				t.Errorf("Received /backfill for the wrong room: %s", remoteRoom.RoomID)
				return util.JSONResponse{
					Code: 400,
					JSON: "wrong room",
				}
			}

			pdusToShare := []json.RawMessage{}
			for _, eventInfo := range eventMap {
				if eventInfo.MessageDraft.ShareInitially {
					pdusToShare = append(pdusToShare, eventInfo.PDU.JSON())
				}
			}

			return util.JSONResponse{
				Code: 200,
				JSON: map[string]interface{}{
					"origin":           srv.ServerName(),
					"origin_server_ts": time.Now().Unix(),
					"pdus":             pdusToShare,
				},
			}
		}),
	).Methods("GET")

	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/event/{eventID}",
		srv.ValidFederationRequest(t, func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
			t.Logf("Got /event for %s (%s)", pathParams["eventID"], eventMap[pathParams["eventID"]].MessageDraft.Message)

			eventInfo, ok := eventMap[pathParams["eventID"]]
			if !ok || !eventInfo.MessageDraft.ShareInitially {
				t.Errorf("Received /event for an unknown event: %s", pathParams["eventID"])
				return util.JSONResponse{
					Code: 400,
					JSON: "unknown event",
				}
			}

			return util.JSONResponse{
				Code: 200,
				JSON: map[string]interface{}{
					"origin":           srv.ServerName(),
					"origin_server_ts": time.Now().Unix(),
					"pdus": []json.RawMessage{
						eventInfo.PDU.JSON(),
					},
				},
			}
		}),
	).Methods("GET")

	// Because state never changes in the room, we can just always respond the same
	//
	// Backfill will cause us to asked about `/state_ids`
	roomStateForMessages := remoteRoom.AllCurrentState()
	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/state_ids/{roomID}",
		srv.ValidFederationRequest(t, func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
			t.Logf("Got /state_ids for %s", pathParams["roomID"])
			if pathParams["roomID"] != remoteRoom.RoomID {
				t.Errorf("Received /state_ids for the wrong room: %s", remoteRoom.RoomID)
				return util.JSONResponse{
					Code: 400,
					JSON: "wrong room",
				}
			}

			return util.JSONResponse{
				Code: 200,
				JSON: struct {
					AuthChainIDs []string `json:"auth_chain_ids"`
					PDUIDs       []string `json:"pdu_ids"`
				}{
					AuthChainIDs: eventIDsFromEvents(remoteRoom.AuthChainForEvents(roomStateForMessages)),
					PDUIDs:       eventIDsFromEvents(roomStateForMessages),
				},
			}
		}),
	).Methods("GET")
	// After asking for `/state_ids`, the homeserver might actually ask about the actual
	// `state` for those event IDs
	srv.Mux().HandleFunc(
		"/_matrix/federation/v1/state/{roomID}",
		srv.ValidFederationRequest(t, func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse {
			t.Logf("Got /state for %s", pathParams["roomID"])
			if pathParams["roomID"] != remoteRoom.RoomID {
				t.Errorf("Received /state for the wrong room: %s", remoteRoom.RoomID)
				return util.JSONResponse{
					Code: 400,
					JSON: "wrong room",
				}
			}

			return util.JSONResponse{
				Code: 200,
				JSON: struct {
					AuthChain gomatrixserverlib.EventJSONs `json:"auth_chain"`
					PDUs      gomatrixserverlib.EventJSONs `json:"pdus"`
				}{
					AuthChain: gomatrixserverlib.NewEventJSONsFromEvents(remoteRoom.AuthChainForEvents(roomStateForMessages)),
					PDUs:      gomatrixserverlib.NewEventJSONsFromEvents(roomStateForMessages),
				},
			}
		}),
	).Methods("GET")

	// The local homeserver joins the room
	alice.MustJoinRoom(t, remoteRoom.RoomID, []spec.ServerName{srv.ServerName()})

	// Backfill the local server with *some* of the messages (leave some gaps)
	// for _, eventID := range slices.Backward(eventIDs) {
	// 	// message := eventMap[eventID].Message
	// 	event := eventMap[eventID].PDU
	// 	// if message.ShareInitially {
	// 	srv.MustSendTransaction(t, deployment, deployment.GetFullyQualifiedHomeserverName(t, "hs1"), []json.RawMessage{event.JSON()}, nil)
	// 	// }
	// }

	messagesRes := alice.MustDo(t, "GET", []string{"_matrix", "client", "r0", "rooms", remoteRoom.RoomID, "messages"},
		client.WithContentType("application/json"),
		client.WithQueries(url.Values{
			"dir":   []string{"b"},
			"limit": []string{"100"},
		}),
	)
	messagesResBody := client.ParseJSON(t, messagesRes)
	t.Logf("asdf %s", messagesResBody)

	fetchUntilMessagesResponseHas(t, alice, remoteRoom.RoomID, func(ev gjson.Result) bool {
		t.Logf("asdf %s %s", ev.Get("event_id").Str, ev.Get("content").Raw)
		return ev.Get("event_id").Str == eventIDs[0]
	})
}

func fetchUntilMessagesResponseHas(t *testing.T, c *client.CSAPI, roomID string, check func(gjson.Result) bool) {
	t.Helper()
	start := time.Now()
	checkCounter := 0
	for {
		if time.Since(start) > c.SyncUntilTimeout {
			t.Fatalf("fetchUntilMessagesResponseHas timed out. Called check function %d times", checkCounter)
		}

		messagesRes := c.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithContentType("application/json"), client.WithQueries(url.Values{
			"dir":   []string{"b"},
			"limit": []string{"100"},
		}))
		messsageResBody := client.ParseJSON(t, messagesRes)
		wantKey := "chunk"
		keyRes := gjson.GetBytes(messsageResBody, wantKey)
		if !keyRes.Exists() {
			t.Fatalf("missing key '%s'", wantKey)
		}
		if !keyRes.IsArray() {
			t.Fatalf("key '%s' is not an array (was %s)", wantKey, keyRes.Type)
		}

		events := keyRes.Array()
		for _, ev := range events {
			if check(ev) {
				return
			}
		}

		checkCounter++
		// Add a slight delay so we don't hammer the messages endpoint
		time.Sleep(500 * time.Millisecond)
	}
}
