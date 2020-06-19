package federation

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"os"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/gomatrixserverlib"
)

// Server represents a federation server
type Server struct {
	t *testing.T

	priv       ed25519.PrivateKey
	keyID      gomatrixserverlib.KeyID
	serverName string

	certPath string
	keyPath  string
	mux      *mux.Router
	srv      *http.Server

	aliases map[string]string
	rooms   map[string]*ServerRoom
	keyRing *gomatrixserverlib.KeyRing
}

// NewServer creates a new federation server with configured options.
func NewServer(t *testing.T, opts ...func(*Server)) *Server {
	// generate signing key
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("federation.NewServer failed to generate ed25519 key: %s", err)
	}

	srv := &Server{
		t:          t,
		priv:       priv,
		keyID:      "ed25519:complement",
		mux:        mux.NewRouter(),
		serverName: "host.docker.internal",
		rooms:      make(map[string]*ServerRoom),
		aliases:    make(map[string]string),
		keyRing: &gomatrixserverlib.KeyRing{
			KeyFetchers: []gomatrixserverlib.KeyFetcher{
				&gomatrixserverlib.DirectKeyFetcher{
					Client: *gomatrixserverlib.NewClient(),
				},
			},
		},
	}

	// generate certs and an http.Server
	httpServer, certPath, keyPath, err := federationServer("name", srv.mux)
	srv.certPath = certPath
	srv.keyPath = keyPath
	srv.srv = httpServer

	for _, opt := range opts {
		opt(srv)
	}
	return srv
}

// UserID returns the complete user ID for the given localpart
func (s *Server) UserID(localpart string) string {
	return fmt.Sprintf("@%s:%s", localpart, s.serverName)
}

// Alias links a room ID and alias localpart together on this server. Returns the full room alias.
func (s *Server) Alias(roomID, aliasLocalpart string) (roomAlias string) {
	roomAlias = fmt.Sprintf("#%s:%s", aliasLocalpart, s.serverName)
	s.aliases[roomAlias] = roomID
	return
}

// MustMakeRoom will add a room to this server so it is accessible to other servers when prompted via federation.
// The `events` will be added to this room. Returns the room ID of the created room.
func (s *Server) MustMakeRoom(t *testing.T, roomVer gomatrixserverlib.RoomVersion, events []b.Event) *ServerRoom {
	roomID := fmt.Sprintf("!%d:%s", len(s.rooms), s.serverName)
	room := &ServerRoom{
		RoomID:  roomID,
		Version: roomVer,
		State:   make(map[string]*gomatrixserverlib.Event),
	}
	// sign all these events
	prevEventID := ""
	for i, ev := range events {
		content, err := json.Marshal(ev.Content)
		if err != nil {
			t.Fatalf("MustMakeRoom: failed to marshal event content %+v", ev.Content)
		}
		eb := gomatrixserverlib.EventBuilder{
			Sender:   ev.Sender,
			Depth:    int64(i + 1), // depth starts at 1
			Type:     ev.Type,
			StateKey: ev.StateKey,
			Content:  content,
			RoomID:   roomID,
		}
		if prevEventID != "" {
			eb.PrevEvents = []string{prevEventID}
		}
		stateNeeded, err := gomatrixserverlib.StateNeededForEventBuilder(&eb)
		if err != nil {
			t.Fatalf("MustMakeRoom: failed to work out auth_events : %s", err)
		}
		eb.AuthEvents = room.AuthEvents(stateNeeded)
		signedEvent, err := eb.Build(time.Now(), gomatrixserverlib.ServerName(s.serverName), s.keyID, s.priv, roomVer)
		if err != nil {
			t.Fatalf("MustMakeRoom: failed to sign event: %s", err)
		}
		room.AddEvent(&signedEvent)
		prevEventID = signedEvent.EventID()
	}
	s.rooms[roomID] = room
	return room
}

// HandleMakeSendJoinRequests is an option which will process make_join and send_join requests for rooms which are present
// in this server. To add a room to this server, see Server.MustMakeRoom. No checks are done to see whether join requests
// are allowed or not. If you wish to test that, write your own test.
func HandleMakeSendJoinRequests() func(*Server) {
	return func(s *Server) {
		s.mux.Handle("/_matrix/federation/v1/make_join/{roomID}/{userID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// Check federation signature
			fedReq, errResp := gomatrixserverlib.VerifyHTTPRequest(
				req, time.Now(), gomatrixserverlib.ServerName(s.serverName), s.keyRing,
			)
			if fedReq == nil {
				w.WriteHeader(errResp.Code)
				b, _ := json.Marshal(errResp.JSON)
				w.Write(b)
				return
			}

			vars := mux.Vars(req)
			userID := vars["userID"]
			roomID := vars["roomID"]

			room, ok := s.rooms[roomID]
			if !ok {
				w.WriteHeader(404)
				w.Write([]byte("complement: make_join unexpected room ID: " + roomID))
				return
			}

			// Generate a join event
			builder := gomatrixserverlib.EventBuilder{
				Sender:     userID,
				RoomID:     roomID,
				Type:       "m.room.member",
				StateKey:   &userID,
				PrevEvents: []string{room.Timeline[len(room.Timeline)-1].EventID()},
			}
			err := builder.SetContent(map[string]interface{}{"membership": gomatrixserverlib.Join})
			if err != nil {
				w.WriteHeader(500)
				w.Write([]byte("complement: make_join cannot set membership content: " + err.Error()))
				return
			}
			stateNeeded, err := gomatrixserverlib.StateNeededForEventBuilder(&builder)
			if err != nil {
				w.WriteHeader(500)
				w.Write([]byte("complement: make_join cannot calculate auth_events: " + err.Error()))
				return
			}
			builder.AuthEvents = room.AuthEvents(stateNeeded)

			// Send it
			res := map[string]interface{}{
				"event":        builder,
				"room_version": room.Version,
			}
			w.WriteHeader(200)
			b, _ := json.Marshal(res)
			w.Write(b)
		})).Methods("GET")

		s.mux.Handle("/_matrix/federation/v1/send_join/{roomID}/{eventID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			fedReq, errResp := gomatrixserverlib.VerifyHTTPRequest(
				req, time.Now(), gomatrixserverlib.ServerName(s.serverName), s.keyRing,
			)
			if fedReq == nil {
				w.WriteHeader(errResp.Code)
				b, _ := json.Marshal(errResp.JSON)
				w.Write(b)
				return
			}
			vars := mux.Vars(req)
			roomID := vars["roomID"]
			//eventID := vars["eventID"]

			_, ok := s.rooms[roomID]
			if !ok {
				w.WriteHeader(404)
				w.Write([]byte("complement: make_join unexpected room ID: " + roomID))
				return
			}

			// TODO: insert join event
		})).Methods("PUT")
	}
}

// HandleKeyRequests is an option which will process GET /_matrix/key/v2/server requests universally when requested.
func HandleKeyRequests() func(*Server) {
	return func(srv *Server) {
		keymux := srv.mux.PathPrefix("/_matrix/key/v2").Subrouter()

		certData, err := ioutil.ReadFile(srv.certPath)
		if err != nil {
			panic("failed to read cert file: " + err.Error())
		}

		keyFn := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			k := gomatrixserverlib.ServerKeys{}
			k.ServerName = gomatrixserverlib.ServerName(srv.serverName)
			publicKey := srv.priv.Public().(ed25519.PublicKey)
			k.VerifyKeys = map[gomatrixserverlib.KeyID]gomatrixserverlib.VerifyKey{
				srv.keyID: {
					Key: gomatrixserverlib.Base64String(publicKey),
				},
			}
			k.OldVerifyKeys = map[gomatrixserverlib.KeyID]gomatrixserverlib.OldVerifyKey{}
			k.TLSFingerprints = fingerprintPEM(certData)
			k.ValidUntilTS = gomatrixserverlib.AsTimestamp(time.Now().Add(24 * time.Hour))
			toSign, err := json.Marshal(k.ServerKeyFields)
			if err != nil {
				w.WriteHeader(500)
				w.Write([]byte(err.Error()))
				return
			}

			k.Raw, err = gomatrixserverlib.SignJSON(
				string(srv.serverName), srv.keyID, srv.priv, toSign,
			)
			if err != nil {
				w.WriteHeader(500)
				w.Write([]byte(err.Error()))
				return
			}
		})

		keymux.Handle("/server", keyFn).Methods("GET")
		keymux.Handle("/server/", keyFn).Methods("GET")
		keymux.Handle("/server/{keyID}", keyFn).Methods("GET")
	}
}

// Mux returns this server's router so you can attach additional paths
func (s *Server) Mux() *mux.Router {
	return s.mux
}

// Listen for federation server requests - call the returned function to gracefully close the server.
func (s *Server) Listen() (cancel func()) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := s.srv.ListenAndServeTLS(s.certPath, s.keyPath)
		if err != nil && err != http.ErrServerClosed {
			s.t.Fatalf("ListenFederationServer: ListenAndServeTLS failed: %s", err)
		}
	}()

	return func() {
		err := s.srv.Shutdown(context.Background())
		if err != nil {
			s.t.Fatalf("ListenFederationServer: failed to shutdown server: %s", err)
		}
		wg.Wait() // wait for the server to shutdown
	}
}

// federationServer creates a federation server with the given handler
func federationServer(name string, h http.Handler) (*http.Server, string, string, error) {
	srv := &http.Server{
		Addr:    ":8448",
		Handler: h,
	}
	tlsCertPath := path.Join(os.TempDir(), "complement.crt")
	tlsKeyPath := path.Join(os.TempDir(), "complement.key")
	certificateDuration := time.Hour
	priv, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, "", "", err
	}
	notBefore := time.Now()
	notAfter := notBefore.Add(certificateDuration)
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, "", "", err
	}

	template := x509.Certificate{
		SerialNumber:          serialNumber,
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, "", "", err
	}
	certOut, err := os.Create(tlsCertPath)
	if err != nil {
		return nil, "", "", err
	}
	defer certOut.Close() // nolint: errcheck
	if err = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return nil, "", "", err
	}

	keyOut, err := os.OpenFile(tlsKeyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return nil, "", "", err
	}
	defer keyOut.Close() // nolint: errcheck
	err = pem.Encode(keyOut, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	})
	if err != nil {
		return nil, "", "", err
	}

	return srv, tlsCertPath, tlsKeyPath, nil
}

func fingerprintPEM(data []byte) []gomatrixserverlib.TLSFingerprint {
	for {
		var certDERBlock *pem.Block
		certDERBlock, data = pem.Decode(data)
		if data == nil {
			return nil
		}
		if certDERBlock.Type == "CERTIFICATE" {
			digest := sha256.Sum256(certDERBlock.Bytes)
			return []gomatrixserverlib.TLSFingerprint{
				{SHA256: digest[:]},
			}
		}
	}
}
