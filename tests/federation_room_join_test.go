package tests

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/matrix-org/gomatrixserverlib"

	"github.com/matrix-org/complement/internal/b"
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
	deployment := Deploy(t, "federation_room_join", b.BlueprintFederationOneToOneRoom)
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
	res, err := bob.DoWithAuthRaw(t, "POST", []string{"_matrix", "client", "r0", "join", serverRoom.RoomID}, nil, "application/json", queryParams)
	must.NotError(t, "failed to join room", err)
	must.MatchResponse(t, res, match.HTTPResponse{
		StatusCode: 200,
		JSON: []match.JSON{
			match.JSONKeyEqual("room_id", serverRoom.RoomID),
		},
	})
}
