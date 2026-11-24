package csapi_tests

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/should"
)

// Maps every object by extracting `type` and `state_key` into a "$type|$state_key" string.
func typeToStateKeyMapper(result gjson.Result) interface{} {
	return strings.Join([]string{result.Map()["type"].Str, result.Map()["state_key"].Str}, "|")
}

// sytest: Can get rooms/{roomId}/members
func TestGetRoomMembers(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	bob.MustJoinRoom(t, roomID, nil)

	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	resp := alice.MustDo(
		t,
		"GET",
		[]string{"_matrix", "client", "v3", "rooms", roomID, "members"},
	)

	must.MatchResponse(t, resp, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONArrayEach("chunk.#.room_id", func(result gjson.Result) error {
				must.Equal(t, result.Str, roomID, "unexpected roomID")
				return nil
			}),
			match.JSONCheckOff("chunk",
				[]interface{}{
					"m.room.member|" + alice.UserID,
					"m.room.member|" + bob.UserID,
				}, match.CheckOffMapper(typeToStateKeyMapper)),
		},
		StatusCode: 200,
	})
}

// Utilize ?at= to get room members at a point in sync.
// sytest: Can get rooms/{roomId}/members at a given point
func TestGetRoomMembersAtPoint(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello world!",
		},
	})

	syncResp, _ := alice.MustSync(t, client.SyncReq{TimeoutMillis: "0"})
	sinceToken := syncResp.Get("rooms.join." + client.GjsonEscape(roomID) + ".timeline.prev_batch").Str

	bob.MustJoinRoom(t, roomID, nil)
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	bob.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello back",
		},
	})

	resp := alice.MustDo(
		t,
		"GET",
		[]string{"_matrix", "client", "v3", "rooms", roomID, "members"},
		client.WithQueries(url.Values{
			"at": []string{sinceToken},
		}),
	)

	must.MatchResponse(t, resp, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONArrayEach("chunk.#.room_id", func(result gjson.Result) error {
				must.Equal(t, result.Str, roomID, "unexpected roomID")
				return nil
			}),
			match.JSONCheckOff("chunk",
				[]interface{}{
					"m.room.member|" + alice.UserID,
				}, match.CheckOffMapper(typeToStateKeyMapper)),
		},

		StatusCode: 200,
	})
}

// sytest: Can filter rooms/{roomId}/members
func TestGetFilteredRoomMembers(t *testing.T) {

	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	bob.MustJoinRoom(t, roomID, nil)

	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	bob.MustLeaveRoom(t, roomID)

	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(bob.UserID, roomID))

	t.Run("not_membership", func(t *testing.T) {
		resp := alice.MustDo(
			t,
			"GET",
			[]string{"_matrix", "client", "v3", "rooms", roomID, "members"},
			client.WithQueries(url.Values{
				"not_membership": []string{"leave"},
			}),
		)

		must.MatchResponse(t, resp, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONArrayEach("chunk.#.room_id", func(result gjson.Result) error {
					must.Equal(t, result.Str, roomID, "unexpected roomID")
					return nil
				}),
				match.JSONCheckOff("chunk",
					[]interface{}{
						"m.room.member|" + alice.UserID,
					}, match.CheckOffMapper(typeToStateKeyMapper)),
			},
			StatusCode: 200,
		})
	})

	t.Run("membership/leave", func(t *testing.T) {
		resp := alice.MustDo(
			t,
			"GET",
			[]string{"_matrix", "client", "v3", "rooms", roomID, "members"},
			client.WithQueries(url.Values{
				"membership": []string{"leave"},
			}),
		)

		must.MatchResponse(t, resp, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONArrayEach("chunk.#.room_id", func(result gjson.Result) error {
					must.Equal(t, result.Str, roomID, "unexpected roomID")
					return nil
				}),
				match.JSONCheckOff("chunk",
					[]interface{}{
						"m.room.member|" + bob.UserID,
					}, match.CheckOffMapper(typeToStateKeyMapper)),
			},
			StatusCode: 200,
		})
	})

	t.Run("membership/join", func(t *testing.T) {
		resp := alice.MustDo(
			t,
			"GET",
			[]string{"_matrix", "client", "v3", "rooms", roomID, "members"},
			client.WithQueries(url.Values{
				"membership": []string{"join"},
			}),
		)

		must.MatchResponse(t, resp, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONArrayEach("chunk.#.room_id", func(result gjson.Result) error {
					must.Equal(t, result.Str, roomID, "unexpected roomID")
					return nil
				}),
				match.JSONCheckOff("chunk",
					[]interface{}{
						"m.room.member|" + alice.UserID,
					}, match.CheckOffMapper(typeToStateKeyMapper)),
			},
			StatusCode: 200,
		})
	})
}

// Same as TestGetRoomMembersAtPoint but we will inject a dangling join event for a remote user.
// Regression test for https://github.com/element-hq/synapse/issues/16940
//
//			     E1
//			   ↗    ↖
//			  |      JOIN EVENT (charlie)
//			  |
//		 -----|---
//			  |
//	          E2
//	          |
//			  E3 <- /members?at=THIS_POINT
func TestGetRoomMembersAtPointWithStateFork(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(nil, nil),
	)
	srv.UnexpectedRequestsAreErrors = false
	cancel := srv.Listen()
	defer cancel()

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := srv.UserID("bob")
	ver := alice.GetDefaultRoomVersion(t)
	serverRoom := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, bob))

	// Join Alice to the new room on the federation server and send E1.
	alice.MustJoinRoom(t, serverRoom.RoomID, []string{srv.ServerName()})
	alice.SendEventSynced(t, serverRoom.RoomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "E1",
		},
	})

	// create JOIN EVENT but don't send it yet, prev_events will be set to [e1]
	charlie := srv.UserID("charlie")
	joinEvent := srv.MustCreateEvent(t, serverRoom, federation.Event{
		Type:     "m.room.member",
		StateKey: b.Ptr(charlie),
		Sender:   charlie,
		Content: map[string]interface{}{
			"membership": "join",
		},
	})

	// send E2 and E3
	alice.SendEventSynced(t, serverRoom.RoomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "E2",
		},
	})
	alice.SendEventSynced(t, serverRoom.RoomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "E3",
		},
	})

	// fork the dag earlier at e1 and send JOIN EVENT
	srv.MustSendTransaction(t, deployment, "hs1", []json.RawMessage{joinEvent.JSON()}, nil)

	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(serverRoom.RoomID, joinEvent.EventID()))

	// now do a sync request with limit=1.
	// Note we don't need to SyncUntil here as we have all the data in the right places already.
	res, nextBatch := alice.MustSync(t, client.SyncReq{
		Filter: `{
			"room": {
				"timeline": {
					"limit": 1
				}
			}
		}`,
	})
	err := should.MatchGJSON(res, match.JSONCheckOff(
		// look in this array
		fmt.Sprintf("rooms.join.%s.state.events", client.GjsonEscape(serverRoom.RoomID)),
		// for these items
		[]interface{}{joinEvent.EventID()},
		// and map them first into this format
		match.CheckOffMapper(func(r gjson.Result) interface{} {
			return r.Get("event_id").Str
		}), match.CheckOffAllowUnwanted(),
	))
	if err != nil {
		t.Logf("did not find charlie's join event in 'state' block: %s", err)
	}
	// now hit /members?at=$nextBatch and check it has the join
	httpRes := alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", serverRoom.RoomID, "members"}, client.WithQueries(map[string][]string{
		"at": {nextBatch},
	}))
	must.MatchResponse(t, httpRes, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONCheckOff("chunk",
				[]interface{}{
					"m.room.member|" + alice.UserID,
					"m.room.member|" + bob,
					"m.room.member|" + charlie,
				}, match.CheckOffMapper(typeToStateKeyMapper)),
		},
		StatusCode: 200,
	})
	// now hit /members?at=$prev_batch and check it has the join
	prevBatch := res.Get(fmt.Sprintf("rooms.join.%s.timeline.prev_batch", client.GjsonEscape(serverRoom.RoomID))).Str
	t.Logf("next_batch=%s prev_batch=%s", nextBatch, prevBatch)
	httpRes = alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", serverRoom.RoomID, "members"}, client.WithQueries(map[string][]string{
		"at": {
			prevBatch,
		},
	}))
	must.MatchResponse(t, httpRes, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONCheckOff("chunk",
				[]interface{}{
					"m.room.member|" + alice.UserID,
					"m.room.member|" + bob,
					"m.room.member|" + charlie,
				}, match.CheckOffMapper(typeToStateKeyMapper)),
		},
		StatusCode: 200,
	})
}
