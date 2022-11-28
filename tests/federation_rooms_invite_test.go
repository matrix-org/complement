package tests

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
)

func TestFederationRoomsInvite(t *testing.T) {
	// deployment := Deploy(t, b.BlueprintFederationOneToOneRoom)
	deployment := Deploy(t, b.BlueprintHSWithApplicationService)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	// bob := deployment.Client(t, "hs1", "@bob:hs1")
	remoteCharlie := deployment.Client(t, "hs2", "@charlie:hs2")
	// remoteFrank := deployment.Client(t, "hs2", "@frank:hs2")

	t.Run("Parallel", func(t *testing.T) {
		t.Run("invite event over federation is seen by application service", func(t *testing.T) {
			t.Parallel()

			// Find the URL and port of the application service in some registration yaml text
			var asURLRegexp = regexp.MustCompile(`url: '(.+):(\d+)'`)

			// Find the application service port defined in the registration file
			asRegistration := deployment.HS["hs2"].ApplicationServices["my_as_on_hs2_id"]
			asURLMatches := asURLRegexp.FindStringSubmatch(asRegistration)
			if asURLMatches == nil {
				t.Fatalf("Unable to find application service `url` in registration=%s", asRegistration)
			}
			asPort := asURLMatches[2]

			// Create a listener and handler to stub an application service listening
			// for transactions from a homeserver.
			handler := mux.NewRouter()
			// Application Service API: /_matrix/app/v1/transactions/{txnId}
			waiter := NewWaiter()
			var eventIDsWeSawOverTransactions []string
			handler.HandleFunc("/transactions/{txnId}", func(w http.ResponseWriter, req *http.Request) {
				must.MatchRequest(t, req, match.HTTPRequest{
					JSON: []match.JSON{
						match.JSONArrayEach("events", func(r gjson.Result) error {
							// Add to our running list of events
							eventIDsWeSawOverTransactions = append(eventIDsWeSawOverTransactions, r.Get("event_id").Str)

							logrus.WithFields(logrus.Fields{
								"event_id":  r.Get("type").Str,
								"state_key": r.Get("state_key").Str,
								"content":   r.Get("content").Raw,
							}).Error("Saw event on application service")

							// If we found the event that occurs after our batch send. we can
							// probably safely assume the historical events won't come later.
							if r.Get("type").Str == "m.room.member" && r.Get("state_key").Str == remoteCharlie.UserID && r.Get("content").Get("membership").Str == "invite" {
								defer waiter.Finish()
							}

							return nil
						}),
					},
				})

				// Acknowledge that we've seen the transaction
				w.WriteHeader(200)
				w.Write([]byte("{}"))
			}).Methods("PUT")

			srv := &http.Server{
				Addr:    fmt.Sprintf(":%s", asPort),
				Handler: handler,
			}
			go func() {
				if err := srv.ListenAndServe(); err != http.ErrServerClosed {
					// Note that running s.t.FailNow is not allowed in a separate goroutine
					// Tests will likely fail if the server is not listening anyways
					t.Logf("Failed to listen and serve our fake application service: %s", err)
				}
			}()
			defer func() {
				err := srv.Shutdown(context.Background())
				if err != nil {
					t.Fatalf("Failed to shutdown our fake application service: %s", err)
				}
			}()
			// ----------------------------------------------------------

			roomID := alice.CreateRoom(t, map[string]interface{}{
				"preset": "private_chat",
				"name":   "Invites room",
				// "invite": []string{bob.UserID},
			})

			// Invite another local user
			// alice.InviteRoom(t, roomID, bob.UserID)

			// Invite some remote users
			alice.InviteRoom(t, roomID, remoteCharlie.UserID)
			// alice.InviteRoom(t, roomID, remoteFrank.UserID)

			wantFields := map[string]string{
				"m.room.join_rules": "join_rule",
				"m.room.name":       "name",
			}
			wantValues := map[string]string{
				"m.room.join_rules": "invite",
				"m.room.name":       "Invites room",
			}

			remoteCharlie.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(remoteCharlie.UserID, roomID))
			res, _ := remoteCharlie.MustSync(t, client.SyncReq{})
			verifyState(t, res, wantFields, wantValues, roomID, alice)

			// If not, wait 5 seconds for to see if it happens. The waiter will only
			// resolve if we see the invite event, otherwise timeout
			waiter.Waitf(t, 5*time.Second, "waiting for invite, eventIDsWeSawOverTransactions=%s", eventIDsWeSawOverTransactions)

			logrus.WithFields(logrus.Fields{
				"eventIDsWeSawOverTransactions": eventIDsWeSawOverTransactions,
			}).Error("afewfeew")
		})
	})
}

// verifyState checks that the fields in "wantFields" are present in invite_state.events
func verifyState(t *testing.T, res gjson.Result, wantFields, wantValues map[string]string, roomID string, cl *client.CSAPI) {
	inviteEvents := res.Get("rooms.invite." + client.GjsonEscape(roomID) + ".invite_state.events")
	if !inviteEvents.Exists() {
		t.Errorf("expected invite events, but they don't exist")
	}
	for _, event := range inviteEvents.Array() {
		eventType := event.Get("type").Str
		field, ok := wantFields[eventType]
		if !ok {
			continue
		}
		wantValue := wantValues[eventType]
		eventStateKey := event.Get("state_key").Str

		res := cl.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", eventType, eventStateKey})

		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyEqual(field, wantValue),
			},
		})
	}
}
