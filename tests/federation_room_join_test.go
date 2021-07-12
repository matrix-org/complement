package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/matrix-org/gomatrix"
	"github.com/matrix-org/gomatrixserverlib"

	"github.com/tidwall/sjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

// This tests that joining a room with ?server_name= works correctly.
// It does this by creating a room on the Complement server and joining HS1 to it.
// The Complement server then begins to refuse make/send_join requests and HS2 is
// asked to join the room ID with the ?server_name=HS1. We need to make sure that
// the HS is not just extracting the domain from the room ID and joining via that,
// hence the refusal for make/send_join on the Complement server.
// We can't use a bogus room ID domain either as auth checks on the
// m.room.create event would pick that up. We also can't tear down the Complement
// server because otherwise signing key lookups will fail.
func TestJoinViaRoomIDAndServerName(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationOneToOneRoom)
	defer deployment.Destroy(t)

	acceptMakeSendJoinRequests := true

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
	)
	srv.UnexpectedRequestsAreErrors = false // we will be sent transactions but that's okay
	cancel := srv.Listen()
	defer cancel()

	srv.Mux().Handle("/_matrix/federation/v1/make_join/{roomID}/{userID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !acceptMakeSendJoinRequests {
			w.WriteHeader(502)
			return
		}
		federation.MakeJoinRequestsHandler(srv, w, req)
	})).Methods("GET")
	srv.Mux().Handle("/_matrix/federation/v2/send_join/{roomID}/{eventID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !acceptMakeSendJoinRequests {
			w.WriteHeader(502)
			return
		}
		federation.SendJoinRequestsHandler(srv, w, req)
	})).Methods("PUT")

	ver := gomatrixserverlib.RoomVersionV5
	charlie := srv.UserID("charlie")
	serverRoom := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, charlie))

	// join the room by room ID, providing the serverName to join via
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	alice.JoinRoom(t, serverRoom.RoomID, []string{srv.ServerName})

	// remove the make/send join paths from the Complement server to force HS2 to join via HS1
	acceptMakeSendJoinRequests = false

	// join the room using ?server_name on HS2
	bob := deployment.Client(t, "hs2", "@bob:hs2")

	queryParams := url.Values{}
	queryParams.Set("server_name", "hs1")
	res := bob.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "join", serverRoom.RoomID}, client.WithQueries(queryParams))
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 200,
		JSON: []match.JSON{
			match.JSONKeyEqual("room_id", serverRoom.RoomID),
		},
	})
}

// This tests that joining a room over federation works in the presence of:
// - Events with missing signatures
// - Events with bad signatures
// - Events with correct signatures but the keys cannot be obtained
// None of these events will be critical to the integrity of the room: that
// is to say these events are never pointed to as auth_events - therefore the
// room should still be joinable.
//
// This test works by creating several federated rooms on Complement which have
// the properties listed above, then asking HS1 to join them and make sure that
// they 200 OK.
func TestJoinFederatedRoomWithUnverifiableEvents(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(nil, nil),
	)
	srv.UnexpectedRequestsAreErrors = false
	cancel := srv.Listen()
	defer cancel()

	ver := gomatrixserverlib.RoomVersionV6
	charlie := srv.UserID("charlie")

	// We explicitly do not run these in parallel in order to help debugging when these
	// tests fail. It doesn't appear to save us much time either!

	t.Run("/send_join response missing signatures shouldn't block room join", func(t *testing.T) {
		//t.Parallel()
		room := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, charlie))
		roomAlias := srv.MakeAliasMapping("MissingSignatures", room.RoomID)
		// create a normal event then remove the signatures key
		signedEvent := srv.MustCreateEvent(t, room, b.Event{
			Sender:   charlie,
			StateKey: b.Ptr(""),
			Type:     "m.room.name",
			Content: map[string]interface{}{
				"name": "This event has no signature",
			},
		})
		raw := signedEvent.JSON()
		raw, err := sjson.SetRawBytes(raw, "signatures", []byte(`{}`))
		must.NotError(t, "failed to strip signatures key from event", err)
		unsignedEvent, err := gomatrixserverlib.NewEventFromTrustedJSON(raw, false, ver)
		must.NotError(t, "failed to make Event from unsigned event JSON", err)
		room.AddEvent(unsignedEvent)
		alice := deployment.Client(t, "hs1", "@alice:hs1")
		alice.JoinRoom(t, roomAlias, nil)
	})
	t.Run("/send_join response with bad signatures shouldn't block room join", func(t *testing.T) {
		//t.Parallel()
		room := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, charlie))
		roomAlias := srv.MakeAliasMapping("BadSignatures", room.RoomID)
		// create a normal event then modify the signatures
		signedEvent := srv.MustCreateEvent(t, room, b.Event{
			Sender:   charlie,
			StateKey: b.Ptr(""),
			Type:     "m.room.name",
			Content: map[string]interface{}{
				"name": "This event has a bad signature",
			},
		})
		newSignaturesBlock := map[string]interface{}{
			docker.HostnameRunningComplement: map[string]string{
				string(srv.KeyID): "/3z+pJjiJXWhwfqIEzmNksvBHCoXTktK/y0rRuWJXw6i1+ygRG/suDCKhFuuz6gPapRmEMPVILi2mJqHHXPKAg",
			},
		}
		rawSig, err := json.Marshal(newSignaturesBlock)
		must.NotError(t, "failed to marshal bad signature block", err)
		raw := signedEvent.JSON()
		raw, err = sjson.SetRawBytes(raw, "signatures", rawSig)
		must.NotError(t, "failed to modify signatures key from event", err)
		unsignedEvent, err := gomatrixserverlib.NewEventFromTrustedJSON(raw, false, ver)
		must.NotError(t, "failed to make Event from unsigned event JSON", err)
		room.AddEvent(unsignedEvent)
		alice := deployment.Client(t, "hs1", "@alice:hs1")
		alice.JoinRoom(t, roomAlias, nil)
	})
	t.Run("/send_join response with unobtainable keys shouldn't block room join", func(t *testing.T) {
		//t.Parallel()
		room := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, charlie))
		roomAlias := srv.MakeAliasMapping("UnobtainableKeys", room.RoomID)
		// create a normal event then modify the signatures to have a bogus key ID which Complement does
		// not have the keys for
		signedEvent := srv.MustCreateEvent(t, room, b.Event{
			Sender:   charlie,
			StateKey: b.Ptr(""),
			Type:     "m.room.name",
			Content: map[string]interface{}{
				"name": "This event has an unobtainable key ID",
			},
		})
		newSignaturesBlock := map[string]interface{}{
			docker.HostnameRunningComplement: map[string]string{
				string(srv.KeyID) + "bogus": "/3z+pJjiJXWhwfqIEzmNksvBHCoXTktK/y0rRuWJXw6i1+ygRG/suDCKhFuuz6gPapRmEMPVILi2mJqHHXPKAg",
			},
		}
		rawSig, err := json.Marshal(newSignaturesBlock)
		must.NotError(t, "failed to marshal bad signature block", err)
		raw := signedEvent.JSON()
		raw, err = sjson.SetRawBytes(raw, "signatures", rawSig)
		must.NotError(t, "failed to modify signatures key from event", err)
		unsignedEvent, err := gomatrixserverlib.NewEventFromTrustedJSON(raw, false, ver)
		must.NotError(t, "failed to make Event from unsigned event JSON", err)
		room.AddEvent(unsignedEvent)
		alice := deployment.Client(t, "hs1", "@alice:hs1")
		alice.JoinRoom(t, roomAlias, nil)
	})
}

// This test checks that users cannot circumvent the auth checks via send_join.
func TestBannedUserCannotSendJoin(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleTransactionRequests(nil, nil),
	)
	cancel := srv.Listen()
	defer cancel()

	fedClient := srv.FederationClient(deployment)

	charlie := srv.UserID("charlie")

	// alice creates a room, and bans charlie from it.
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	roomID := alice.CreateRoom(t, map[string]interface{}{})

	alice.SendEventSynced(t, roomID, b.Event{
		Type:     "m.room.member",
		Sender:   alice.UserID,
		StateKey: &charlie,
		Content: map[string]interface{}{
			"membership": "ban",
		},
	})

	// charlie sends a make_join for a different user
	makeJoinResp, err := fedClient.MakeJoin(context.Background(), "hs1", roomID, srv.UserID("charlie2"), federation.SupportedRoomVersions())
	must.NotError(t, "MakeJoin", err)

	// ... and does a switcheroo to turn it into a join for himself
	makeJoinResp.JoinEvent.Sender = charlie
	makeJoinResp.JoinEvent.StateKey = &charlie
	joinEvent, err := makeJoinResp.JoinEvent.Build(time.Now(), gomatrixserverlib.ServerName(srv.ServerName), srv.KeyID, srv.Priv, makeJoinResp.RoomVersion)
	must.NotError(t, "JoinEvent.Build", err)

	// SendJoin should return a 403.
	_, err = fedClient.SendJoin(context.Background(), "hs1", joinEvent, makeJoinResp.RoomVersion)
	if err == nil {
		t.Errorf("SendJoin returned 200")
	} else if httpError, ok := err.(gomatrix.HTTPError); ok {
		t.Logf("SendJoin => %d/%s", httpError.Code, string(httpError.Contents))
		if httpError.Code != 403 {
			t.Errorf("expected 403, got %d", httpError.Code)
		}
		errcode := must.GetJSONFieldStr(t, httpError.Contents, "errcode")
		if errcode != "M_FORBIDDEN" {
			t.Errorf("errcode: got %s, want M_FORBIDDEN", errcode)
		}
	} else {
		t.Errorf("SendJoin: non-HTTPError: %v", err)
	}

	// Alice checks the room state to check that charlie isn't a member
	res := alice.MustDoFunc(
		t,
		"GET",
		[]string{"_matrix", "client", "r0", "rooms", roomID, "state", "m.room.member", charlie},
	)
	stateResp := client.ParseJSON(t, res)
	membership := must.GetJSONFieldStr(t, stateResp, "membership")
	must.EqualStr(t, membership, "ban", "membership of charlie")
}
