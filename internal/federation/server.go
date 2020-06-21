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
	"math/big"
	"net/http"
	"os"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/docker"
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
func NewServer(t *testing.T, deployment *docker.Deployment, opts ...func(*Server)) *Server {
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
	}
	fetcher := &basicKeyFetcher{
		KeyFetcher: &gomatrixserverlib.DirectKeyFetcher{
			Client: *gomatrixserverlib.NewClientWithTransport(&docker.RoundTripper{
				Deployment: deployment,
			}),
		},
		srv: srv,
	}
	srv.keyRing = &gomatrixserverlib.KeyRing{
		KeyDatabase: &nopKeyDatabase{},
		KeyFetchers: []gomatrixserverlib.KeyFetcher{
			fetcher,
		},
	}
	srv.mux.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		t.Logf("received unexpected request to server: %s %s", req.Method, req.URL.Path)
		w.WriteHeader(404)
		w.Write([]byte("complement: federation server is not listening for this path"))
	})

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
// This assumes that this server is joined to the given room ID.
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

type nopKeyDatabase struct {
	gomatrixserverlib.KeyFetcher
}

func (d *nopKeyDatabase) StoreKeys(ctx context.Context, results map[gomatrixserverlib.PublicKeyLookupRequest]gomatrixserverlib.PublicKeyLookupResult) error {
	return nil
}
func (f *nopKeyDatabase) FetchKeys(
	ctx context.Context,
	requests map[gomatrixserverlib.PublicKeyLookupRequest]gomatrixserverlib.Timestamp) (
	map[gomatrixserverlib.PublicKeyLookupRequest]gomatrixserverlib.PublicKeyLookupResult, error,
) {
	return nil, nil
}
func (f *nopKeyDatabase) FetcherName() string {
	return "nopKeyDatabase"
}

type basicKeyFetcher struct {
	gomatrixserverlib.KeyFetcher
	srv *Server
}

func (f *basicKeyFetcher) FetchKeys(
	ctx context.Context,
	requests map[gomatrixserverlib.PublicKeyLookupRequest]gomatrixserverlib.Timestamp) (
	map[gomatrixserverlib.PublicKeyLookupRequest]gomatrixserverlib.PublicKeyLookupResult, error,
) {
	result := make(map[gomatrixserverlib.PublicKeyLookupRequest]gomatrixserverlib.PublicKeyLookupResult, len(requests))
	for req := range requests {
		if string(req.ServerName) == f.srv.serverName && req.KeyID == f.srv.keyID {
			publicKey := f.srv.priv.Public().(ed25519.PublicKey)
			result[req] = gomatrixserverlib.PublicKeyLookupResult{
				ValidUntilTS: gomatrixserverlib.AsTimestamp(time.Now().Add(24 * time.Hour)),
				ExpiredTS:    gomatrixserverlib.PublicKeyNotExpired,
				VerifyKey: gomatrixserverlib.VerifyKey{
					Key: gomatrixserverlib.Base64Bytes(publicKey),
				},
			}
		} else {
			return f.KeyFetcher.FetchKeys(ctx, requests)
		}
	}
	return result, nil
}

func (f *basicKeyFetcher) FetcherName() string {
	return "basicKeyFetcher"
}
