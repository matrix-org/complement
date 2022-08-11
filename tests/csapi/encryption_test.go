package csapi_tests

import (
	"reflect"
	"testing"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
)

func TestEncryption(t *testing.T) {
	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	encAlice := client.MustCreateEncryptedClient(t, alice)
	encBob := client.MustCreateEncryptedClient(t, bob)

	// Create a new room and invite Bob
	roomResp, err := encAlice.Client.CreateRoom(&mautrix.ReqCreateRoom{
		Invite: []id.UserID{id.UserID(bob.UserID)},
		Preset: "private_chat",
	})
	if err != nil {
		t.Fatalf("failed to create room: %s", err)
	}
	roomID := string(roomResp.RoomID)

	// Wait for invite and join Bob
	bob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
	bob.JoinRoom(t, roomID, []string{})
	encAlice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))
	encBob.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	// start syncing using mautrix.Client
	go encAlice.Sync(t)
	go encBob.Sync(t)
	defer encAlice.StopSync()
	defer encBob.StopSync()

	// Enable encryption
	resp, err := encAlice.Client.SendStateEvent(roomResp.RoomID, event.StateEncryption, "", event.EncryptionEventContent{Algorithm: id.AlgorithmMegolmV1})
	if err != nil {
		t.Fatalf("failed to enable encryption: %v", err)
	}

	// Wait for the event to come down sync using Complements client
	encAlice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, string(resp.EventID)))
	encBob.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, string(resp.EventID)))

	time.Sleep(time.Second)

	// Send an encrypted message using mautrix.Client
	wantMessage := &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    "Hello world!",
	}
	// This sends an encrypted message
	sendMessageResp, err := encAlice.SendEncryptedMessage(roomResp.RoomID, wantMessage)
	if err != nil {
		t.Fatalf("failed to send message event: %s", err)
	}
	time.Sleep(time.Second)

	// Wait for the message to come down sync using Complements client
	encBob.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, string(sendMessageResp.EventID)))
	encAlice.MustSyncUntil(t, client.SyncReq{}, client.SyncTimelineHasEventID(roomID, string(sendMessageResp.EventID)))

	// Wait for an encrypted event with Bob
	t.Log("Waiting for encrypted event")

	select {
	case res := <-encBob.WaitForEnc():
		if res.Error != nil {
			t.Fatal(res.Error)
		}
		gotMessage := res.Event.Content.AsMessage()
		if !reflect.DeepEqual(wantMessage, gotMessage) {
			t.Fatalf("Expected\n%+v\ngot\n%+v\n", wantMessage, gotMessage)
		}

		t.Logf("Successfully decrypted message: %s", gotMessage.Body)

	case <-time.After(time.Second * 5):
		t.Fatalf("timed out waiting for encrypted event")
	}

}
