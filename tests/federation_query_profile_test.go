package tests

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

// TODO:
// Inbound federation can query profile data

// Test that the server can make outbound federation profile requests
// https://matrix.org/docs/spec/server_server/latest#get-matrix-federation-v1-query-profile
func TestOutboundFederationProfile(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
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
		unauthedClient := deployment.Client(t, "hs1", "")
		res := unauthedClient.MustDo(t, "GET", []string{"_matrix", "client", "r0", "profile", remoteUserID, "displayname"}, nil)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual("displayname", remoteDisplayName),
			},
		})
	})
}
