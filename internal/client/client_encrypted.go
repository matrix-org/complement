package client

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/tidwall/gjson"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/matrix-org/complement/internal/must"
)

type CSAPIEncrypted struct {
	*CSAPI
	mautrix    *mautrix.Client
	olmMachine *crypto.OlmMachine
	stateStore StateStore
	encChan    chan EncryptedEventResponse
}

type EncryptedEventResponse struct {
	Body  string
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

	stateStore := StateStore{storer: mautrix.NewInMemoryStore()}

	// Open SQL connection
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
		mautrix:    cl,
		stateStore: stateStore,
		encChan:    make(chan EncryptedEventResponse, 1),
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

// SendEncryptedMessageSynced sends the given content to the given room ID using mautrix.Client.
// If the target room has enabled encryption, a megolm session is created if one doesn't already exist
// and the message is sent after being encrypted. If the room isn't encrypted or a new megolm session
// can't be created, fails the test.
func (enc *CSAPIEncrypted) SendEncryptedMessageSynced(
	t *testing.T,
	roomID string,
	msgType, body string,
) string {
	room := id.RoomID(roomID)
	olmMachine := enc.olmMachine

	// We always want to send encrypted message, so return an error if the room isn't encrypted
	if !olmMachine.StateStore.IsEncrypted(id.RoomID(roomID)) {
		t.Fatal("expected to find encrypted room, but didn't")
	}

	// Check if there is already a megolm session
	if sess, err := olmMachine.CryptoStore.GetOutboundGroupSession(room); err != nil {
		return ""
	} else if sess == nil || sess.Expired() || !sess.Shared {
		// No error but valid, shared session does not exist
		joinedMembersResp := enc.MustDoFunc(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "joined_members"})
		joinedMembersData := must.ParseJSON(t, joinedMembersResp.Body)
		joined := gjson.GetBytes(joinedMembersData, "joined|@keys").Array()
		memberIDs := make([]id.UserID, 0, len(joined))
		for _, userID := range joined {
			memberIDs = append(memberIDs, id.UserID(userID.Str))
		}

		// Share group session with room members
		if err = olmMachine.ShareGroupSession(room, memberIDs); err != nil {
			t.Fatal(err)
		}
	}
	msg := &event.MessageEventContent{
		MsgType: event.MessageType(msgType),
		Body:    body,
	}
	content, err := olmMachine.EncryptMegolmEvent(room, event.EventMessage, msg)
	if err != nil {
		t.Fatal(err)
	}
	event, err := enc.mautrix.SendMessageEvent(room, event.EventEncrypted, content)
	if err != nil {
		t.Fatal(err)
	}
	eventID := string(event.EventID)
	enc.MustSyncUntil(t, SyncReq{}, SyncTimelineHasEventID(roomID, eventID))

	return eventID
}

func (enc *CSAPIEncrypted) EncryptedMessage() chan EncryptedEventResponse {
	return enc.encChan
}

// encryptedEventHandler handles encrypted message events. Sends the output EncryptedEventResponse to the created channel.
func (enc *CSAPIEncrypted) encryptedEventHandler() mautrix.EventHandler {
	return func(source mautrix.EventSource, evt *event.Event) {
		decrypted, err := enc.olmMachine.DecryptMegolmEvent(evt)
		resp := EncryptedEventResponse{}
		if err != nil {
			resp.Error = err
		} else {
			fmt.Printf("Decrypted: %+v\n", decrypted.Content.AsMessage())
			resp.Body = decrypted.Content.AsMessage().Body
		}
		enc.encChan <- resp
	}
}

// Sync loops to keep syncing the client with the homeserver by calling the /sync endpoint.
func (enc *CSAPIEncrypted) Sync(t *testing.T) {
	// Get the state store up to date
	resp, err := enc.mautrix.SyncRequest(30000, "", "", true, event.PresenceOnline, context.TODO())
	if err != nil {
		t.Fatalf("initial sync failed: %s", err)
		return
	}
	enc.stateStore.UpdateStateStore(resp)

	if e := enc.mautrix.Sync(); e != nil {
		t.Fatalf("failed to Sync(): %s", e)
	} else {
		t.Logf("Stopping Sync() for %s", enc.mautrix.UserID)
	}
}

func (enc *CSAPIEncrypted) StopSync() {
	enc.mautrix.StopSync()
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
