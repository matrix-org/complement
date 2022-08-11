package csapi_tests

import (
	"reflect"
	"testing"
	"time"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
)

func TestEncryption(t *testing.T) {
	emptyString := ""

	deployment := Deploy(t, b.BlueprintOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs1", "@bob:hs1")

	encAlice := client.MustCreateEncryptedClient(t, alice)
	encBob := client.MustCreateEncryptedClient(t, bob)

	// start syncing using mautrix.Client, needed to update the olmMachine store
	go encAlice.Sync(t)
	go encBob.Sync(t)
	defer encAlice.StopSync()
	defer encBob.StopSync()

	// Create a new room and invite Bob
	roomID := encAlice.CreateRoom(t, map[string]interface{}{
		"invite": []string{bob.UserID},
		"preset": "private_chat",
	})
	// Wait for invite and join Bob
	encBob.MustSyncUntil(t, client.SyncReq{}, client.SyncInvitedTo(bob.UserID, roomID))
	encBob.JoinRoom(t, roomID, []string{})
	encAlice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(bob.UserID, roomID))

	// Enable encryption
	encAlice.SendEventSynced(t, roomID, b.Event{
		Type:     "m.room.encryption",
		StateKey: &emptyString,
		Sender:   encAlice.UserID,
		Content: map[string]interface{}{
			"algorithm": "m.megolm.v1.aes-sha2",
		},
	})

	// Send an encrypted text message
	msgType := "m.text"
	wantBody := "Hello World!"
	encAlice.SendEncryptedMessageSynced(t, roomID, msgType, wantBody)

	// Wait for an encrypted event with Bob
	t.Log("Waiting for encrypted event")
	select {
	case res := <-encBob.EncryptedMessage():
		if res.Error != nil {
			t.Fatal(res.Error)
		}
		gotBody := res.Body
		if !reflect.DeepEqual(wantBody, gotBody) {
			t.Fatalf("Expected\n%+v\ngot\n%+v\n", wantBody, gotBody)
		}

		t.Logf("Successfully decrypted message: %s", gotBody)

	case <-time.After(time.Second * 5):
		t.Fatalf("timed out waiting for encrypted event")
	}

}
