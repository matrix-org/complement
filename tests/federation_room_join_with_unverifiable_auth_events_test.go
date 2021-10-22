// +build !dendrite_blacklist

package tests

import (
	"encoding/json"
	"testing"

	"github.com/matrix-org/gomatrixserverlib"
	"github.com/tidwall/sjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/complement/internal/must"
)

// This test is an extension of TestJoinFederatedRoomWithUnverifiableEvents.
// It's currently split out, because it fails on Dendrite
//  - see https://github.com/matrix-org/dendrite/issues/2028
func TestJoinFederatedRoomWithUnverifiableAuthEvents(t *testing.T) {
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

	t.Run("/send_join response with state with unverifiable auth events shouldn't block room join", func(t *testing.T) {
		//t.Parallel()
		room := srv.MustMakeRoom(t, ver, federation.InitialRoomEvents(ver, charlie))
		roomAlias := srv.MakeAliasMapping("UnverifiableAuthEvents", room.RoomID)

		// create a normal event then modify the signatures
		rawEvent := srv.MustCreateEvent(t, room, b.Event{
			Sender:   charlie,
			StateKey: &charlie,
			Type:     "m.room.member",
			Content: map[string]interface{}{
				"membership": "join",
				"name":       "This event has a bad signature",
			},
		}).JSON()
		rawSig, err := json.Marshal(map[string]interface{}{
			docker.HostnameRunningComplement: map[string]string{
				string(srv.KeyID): "/3z+pJjiJXWhwfqIEzmNksvBHCoXTktK/y0rRuWJXw6i1+ygRG/suDCKhFuuz6gPapRmEMPVILi2mJqHHXPKAg",
			},
		})
		must.NotError(t, "failed to marshal bad signature block", err)
		rawEvent, err = sjson.SetRawBytes(rawEvent, "signatures", rawSig)
		must.NotError(t, "failed to modify signatures key from event", err)
		badlySignedEvent, err := gomatrixserverlib.NewEventFromTrustedJSON(rawEvent, false, ver)
		must.NotError(t, "failed to make Event from badly signed event JSON", err)
		room.AddEvent(badlySignedEvent)
		t.Logf("Created badly signed auth event %s", badlySignedEvent.EventID())

		// and now add another event which will use it as an auth event.
		goodEvent := srv.MustCreateEvent(t, room, b.Event{
			Sender:   charlie,
			StateKey: &charlie,
			Type:     "m.room.member",
			Content: map[string]interface{}{
				"membership": "leave",
			},
		})
		// double-check that the bad event is in its auth events
		containsEvent := false
		for _, authEventID := range goodEvent.AuthEventIDs() {
			if authEventID == badlySignedEvent.EventID() {
				containsEvent = true
				break
			}
		}
		if !containsEvent {
			t.Fatalf("Bad event didn't appear in auth events of state event")
		}
		room.AddEvent(goodEvent)
		t.Logf("Created state event %s", goodEvent.EventID())

		alice := deployment.Client(t, "hs1", "@alice:hs1")
		alice.JoinRoom(t, roomAlias, nil)
	})
}
