package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/matrix-org/gomatrixserverlib/spec"

	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

// TODO:
// Inbound federation can query profile data

// Test that the server can make outbound federation profile requests
// https://matrix.org/docs/spec/server_server/latest#get-matrix-federation-v1-query-profile
func TestOutboundFederationProfile(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
	)
	cancel := srv.Listen()
	defer cancel()

	// sytest: Outbound federation can query profile data
	t.Run("Outbound federation can query profile data", func(t *testing.T) {
		remoteUserID := srv.UserID("user")
		remoteDisplayName := "my remote display name"

		srv.Mux().Handle("/_matrix/federation/v1/query/profile", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			userID := req.URL.Query().Get("user_id")
			if userID != remoteUserID {
				w.WriteHeader(500)
				t.Fatalf("GET /_matrix/federation/v1/query/profile with wrong user ID, got '%s' want '%s'", userID, remoteUserID)
				return
			}
			resBody, err := json.Marshal(struct {
				Displayname string `json:"displayname"`
				AvatarURL   string `json:"avatar_url"`
			}{
				remoteDisplayName, "",
			})
			if err != nil {
				w.WriteHeader(500)
				t.Fatalf("GET /_matrix/federation/v1/query/profile failed to marshal response: %s", err)
				return
			}
			w.WriteHeader(200)
			w.Write(resBody)
		})).Methods("GET")

		// query the display name which should do an outbound federation hit
		unauthedClient := deployment.UnauthenticatedClient(t, "hs1")
		gotDisplayName := unauthedClient.MustGetDisplayName(t, remoteUserID)
		must.Equal(t, gotDisplayName, remoteDisplayName, "display name mismatch")
	})
}

func TestInboundFederationProfile(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
	)
	cancel := srv.Listen()
	defer cancel()
	origin := spec.ServerName(srv.ServerName())

	// sytest: Non-numeric ports in server names are rejected
	t.Run("Non-numeric ports in server names are rejected", func(t *testing.T) {
		fedReq := fclient.NewFederationRequest(
			"GET",
			origin,
			"hs1",
			"/_matrix/federation/v1/query/profile"+
				"?user_id=@user1:localhost:http"+
				"&field=displayname",
		)

		resp, err := srv.DoFederationRequest(context.Background(), t, deployment, fedReq)

		must.NotError(t, "failed to GET /profile", err)

		must.MatchResponse(t, resp, match.HTTPResponse{
			StatusCode: 400,
		})
	})

	// sytest: Inbound federation can query profile data
	t.Run("Inbound federation can query profile data", func(t *testing.T) {
		const alicePublicName = "Alice Cooper"

		alice.MustSetDisplayName(t, alicePublicName)

		fedReq := fclient.NewFederationRequest(
			"GET",
			origin,
			"hs1",
			"/_matrix/federation/v1/query/profile"+
				"?user_id="+alice.UserID+
				"&field=displayname",
		)

		resp, err := srv.DoFederationRequest(context.Background(), t, deployment, fedReq)

		must.NotError(t, "failed to GET /profile", err)

		must.MatchResponse(t, resp, match.HTTPResponse{
			StatusCode: 200,
			JSON: []match.JSON{
				match.JSONKeyEqual("displayname", alicePublicName),
			},
		})
	})
}
