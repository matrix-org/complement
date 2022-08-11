package client

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type CSAPIEncrypted struct {
	*CSAPI
	*mautrix.Client
	olmMachine *crypto.OlmMachine
	stateStore StateStore
	encChan    chan EncEvResp
}

type EncEvResp struct {
	Event *event.Event
	Error error
}

type clientOptions struct {
	logger crypto.Logger
}

type ClientOption func(*clientOptions)

// WithLogging enables a crypto.Logger using t.Log/t.Fatal etc.
func WithLogging(t *testing.T, csapi *CSAPI) ClientOption {
	return func(opts *clientOptions) {
		opts.logger = testCryptoLogger{
			t:      t,
			userID: csapi.UserID,
		}
	}
}

// MustCreateEncryptedClient creates an "encrypted client" from a CSAPI and initialises
// the OlmMachine, fails the test if the client or OlmMachine can't be created
func MustCreateEncryptedClient(t *testing.T, csapi *CSAPI, opts ...ClientOption) *CSAPIEncrypted {
	defaultOptions := &clientOptions{
		logger: emptyCryptoLogger{},
	}

	for _, opt := range opts {
		opt(defaultOptions)
	}

	cl, err := mautrix.NewClient(csapi.BaseURL, id.UserID(csapi.UserID), csapi.AccessToken)
	if err != nil {
		t.Fatalf("failed to create mautrix.Client: %s", err)
	}
	cl.DeviceID = id.DeviceID(csapi.DeviceID)

	stateStore := StateStore{Storer: mautrix.NewInMemoryStore()}

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sql.DB: %s", err)
	}

	accountID := csapi.UserID + "-" + csapi.DeviceID
	sqlCryptoStore := crypto.NewSQLCryptoStore(db, "sqlite3", accountID, id.DeviceID(csapi.DeviceID), []byte(csapi.DeviceID+"pickle"), defaultOptions.logger)

	// Create needed tables
	if err = sqlCryptoStore.CreateTables(); err != nil {
		t.Fatalf("unable to create crypto store tables: %s", err)
	}

	olmMachine := crypto.NewOlmMachine(cl, defaultOptions.logger, sqlCryptoStore, &stateStore)

	encAPI := &CSAPIEncrypted{
		CSAPI:      csapi,
		olmMachine: olmMachine,
		Client:     cl,
		stateStore: stateStore,
		encChan:    make(chan EncEvResp, 1),
	}

	// Accept all incoming verification requests
	/*olmMachine.AcceptVerificationFrom = func(_ string, otherDevice *crypto.DeviceIdentity, _ id.RoomID) (crypto.VerificationRequestResponse, crypto.VerificationHooks) {
		return crypto.AcceptRequest, encAPI
	}*/

	syncer := cl.Syncer.(*mautrix.DefaultSyncer)
	// Handle general events, e.g. to update the olmMachine and stateStore
	syncer.OnSync(encAPI.syncCallback(t))

	// Handle membership events
	syncer.OnEventType(event.StateMember, func(_ mautrix.EventSource, evt *event.Event) {
		encAPI.olmMachine.HandleMemberEvent(evt)
	})

	// Handle encrypted events
	syncer.OnEventType(event.EventEncrypted, encAPI.encryptedEventHandler())

	if err := olmMachine.Load(); err != nil {
		t.Fatalf("failed to load olmMachine: %s", err)
	}

	return encAPI
}

// SendEncryptedMessage sends the given content to the given room ID using mautrix.Client.
// If the target room has enabled encryption, a megolm session is created if one doesn't already exist
// and the message is sent after being encrypted. If the room isn't encrypted or a new megolm session
// can't be created, returns an error.
func (enc *CSAPIEncrypted) SendEncryptedMessage(
	roomID id.RoomID,
	content interface{},
	extra ...mautrix.ReqSendEvent,
) (*mautrix.RespSendEvent, error) {

	olmMachine := enc.olmMachine

	// We always want to send encrypted message, so return an error if the room isn't encrypted
	if !olmMachine.StateStore.IsEncrypted(roomID) {
		return nil, fmt.Errorf("expected to find encrypted room, but didn't")
	}

	// Check if there is already a megolm session
	if sess, err := olmMachine.CryptoStore.GetOutboundGroupSession(roomID); err != nil {
		return nil, err
	} else if sess == nil || sess.Expired() || !sess.Shared {
		// No error but valid, shared session does not exist
		memberIDs, err := enc.stateStore.GetJoinedMembers(roomID)
		if err != nil {
			return nil, err
		}
		// Share group session with room members
		if err = olmMachine.ShareGroupSession(roomID, memberIDs); err != nil {
			return nil, err
		}
	}
	content, err := olmMachine.EncryptMegolmEvent(roomID, event.EventMessage, content)
	if err != nil {
		return nil, err
	}

	return enc.Client.SendMessageEvent(roomID, event.EventEncrypted, content, extra...)
}

func (enc *CSAPIEncrypted) WaitForEnc() chan EncEvResp {
	return enc.encChan
}

// encryptedEventHandler handles encrypted message events. Sends the output EncEvResp to the created channel.
func (enc *CSAPIEncrypted) encryptedEventHandler() mautrix.EventHandler {
	return func(source mautrix.EventSource, evt *event.Event) {
		decrypted, err := enc.olmMachine.DecryptMegolmEvent(evt)
		resp := EncEvResp{
			Event: decrypted,
		}
		if err != nil {
			resp.Error = err
		}
		enc.encChan <- resp
	}
}

// Sync loops to keep syncing the client with the homeserver by calling the /sync endpoint.
func (enc *CSAPIEncrypted) Sync(t *testing.T) {
	// Get the state store up to date
	resp, err := enc.Client.SyncRequest(30000, "", "", true, event.PresenceOnline, context.TODO())
	if err != nil {
		t.Fatalf("initial sync failed: %s", err)
		return
	}
	enc.stateStore.UpdateStateStore(resp)

	if e := enc.Client.Sync(); e != nil {
		t.Fatalf("failed to Sync(): %s", e)
	} else {
		t.Logf("Stopping Sync() for %s", enc.Client.UserID)
		return
	}
}

// syncCallback updates the stateStore as well as olmMachine required
// information (e.g. handles device lists and to-device events)
func (enc *CSAPIEncrypted) syncCallback(t *testing.T) mautrix.SyncHandler {
	return func(resp *mautrix.RespSync, since string) bool {
		enc.stateStore.UpdateStateStore(resp)
		enc.olmMachine.ProcessSyncResponse(resp, since)
		if err := enc.olmMachine.CryptoStore.Flush(); err != nil {
			t.Fatalf("Could not flush crypto store: %s", err)
		}
		return true
	}
}
