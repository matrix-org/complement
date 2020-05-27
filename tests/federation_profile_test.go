package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/federation"
)

// Test that the server can make outbound federation profile requests
// https://matrix.org/docs/spec/server_server/latest#get-matrix-federation-v1-query-profile
func TestOutboundFederationProfile(t *testing.T) {
	deployment := MustDeploy(t, "federation_profile", b.BlueprintOneToOneRoom.Name)
	defer deployment.Destroy()

	srv := federation.NewServer(t,
		federation.HandleKeyRequests(),
	)
	cancel := srv.Listen()
	defer cancel()

	remoteUserID := "@user:host.docker.internal"
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
	x := deployment.HS["hs1"].BaseURL + "/_matrix/client/r0/profile/" + url.PathEscape(remoteUserID) + "/displayname"
	fmt.Println(x)
	res, err := http.Get(x)
	MustNotError(t, "GET /profile returned error: %s", err)
	MustHaveStatus(t, res, 200)
	body := MustParseJSON(t, res)
	MustHaveJSONKeyEqual(t, body, "displayname", remoteDisplayName)
}
