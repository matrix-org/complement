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
	"errors"
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
	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/gomatrixserverlib"
)

// Server represents a federation server
type Server struct {
	t *testing.T

	// Default: true
	UnexpectedRequestsAreErrors bool

	Priv       ed25519.PrivateKey
	KeyID      gomatrixserverlib.KeyID
	ServerName string

	certPath string
	keyPath  string
	mux      *mux.Router
	srv      *http.Server

	directoryHandlerSetup bool
	aliases               map[string]string
	rooms                 map[string]*ServerRoom
	keyRing               *gomatrixserverlib.KeyRing
}

// NewServer creates a new federation server with configured options.
func NewServer(t *testing.T, deployment *docker.Deployment, opts ...func(*Server)) *Server {
	// generate signing key
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("federation.NewServer failed to generate ed25519 key: %s", err)
	}

	srv := &Server{
		t:                           t,
		Priv:                        priv,
		KeyID:                       "ed25519:complement",
		mux:                         mux.NewRouter(),
		ServerName:                  docker.HostnameRunningComplement,
		rooms:                       make(map[string]*ServerRoom),
		aliases:                     make(map[string]string),
		UnexpectedRequestsAreErrors: true,
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
	srv.mux.Use(func(h http.Handler) http.Handler {
		// Return a json Content-Type header to all requests by default
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Content-Type", "application/json")
			h.ServeHTTP(w, r)
		})
	})
	srv.mux.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if srv.UnexpectedRequestsAreErrors {
			t.Errorf("Server.UnexpectedRequestsAreErrors=true received unexpected request to server: %s %s", req.Method, req.URL.Path)
		} else {
			t.Logf("Server.UnexpectedRequestsAreErrors=false received unexpected request to server: %s %s", req.Method, req.URL.Path)
		}
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
	return fmt.Sprintf("@%s:%s", localpart, s.ServerName)
}

// MakeAliasMapping will create a mapping of room alias to room ID on this server. Returns the alias.
// If this is the first time calling this function, a directory lookup handler will be added to
// handle alias requests over federation.
func (s *Server) MakeAliasMapping(aliasLocalpart, roomID string) string {
	alias := fmt.Sprintf("#%s:%s", aliasLocalpart, s.ServerName)
	s.aliases[alias] = roomID
	HandleDirectoryLookups()(s)
	return alias
}

// MustMakeRoom will add a room to this server so it is accessible to other servers when prompted via federation.
// The `events` will be added to this room. Returns the created room.
func (s *Server) MustMakeRoom(t *testing.T, roomVer gomatrixserverlib.RoomVersion, events []b.Event) *ServerRoom {
	roomID := fmt.Sprintf("!%d:%s", len(s.rooms), s.ServerName)
	room := &ServerRoom{
		RoomID:  roomID,
		Version: roomVer,
		State:   make(map[string]*gomatrixserverlib.Event),
	}
	// sign all these events
	for _, ev := range events {
		signedEvent := s.MustCreateEvent(t, room, ev)
		room.AddEvent(signedEvent)
	}
	s.rooms[roomID] = room
	return room
}

// FederationClient returns a client which will sign requests using this server and accept certs from hsName.
func (s *Server) FederationClient(deployment *docker.Deployment, hsName string) *gomatrixserverlib.FederationClient {
	f := gomatrixserverlib.NewFederationClient(gomatrixserverlib.ServerName(s.ServerName), s.KeyID, s.Priv)
	f.Client = *gomatrixserverlib.NewClientWithTransport(&docker.RoundTripper{deployment})
	return f
}

// MustCreateEvent will create and sign a new latest event for the given room.
// It does not insert this event into the room however. See ServerRoom.AddEvent for that.
func (s *Server) MustCreateEvent(t *testing.T, room *ServerRoom, ev b.Event) *gomatrixserverlib.Event {
	t.Helper()
	content, err := json.Marshal(ev.Content)
	if err != nil {
		t.Fatalf("MustCreateEvent: failed to marshal event content %+v", ev.Content)
	}
	eb := gomatrixserverlib.EventBuilder{
		Sender:     ev.Sender,
		Depth:      int64(room.Depth + 1), // depth starts at 1
		Type:       ev.Type,
		StateKey:   ev.StateKey,
		Content:    content,
		RoomID:     room.RoomID,
		PrevEvents: room.ForwardExtremities,
	}
	stateNeeded, err := gomatrixserverlib.StateNeededForEventBuilder(&eb)
	if err != nil {
		t.Fatalf("MustCreateEvent: failed to work out auth_events : %s", err)
	}
	eb.AuthEvents = room.AuthEvents(stateNeeded)
	signedEvent, err := eb.Build(time.Now(), gomatrixserverlib.ServerName(s.ServerName), s.KeyID, s.Priv, room.Version)
	if err != nil {
		t.Fatalf("MustCreateEvent: failed to sign event: %s", err)
	}
	return &signedEvent
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

// Get or create local CA cert. This is used to create the federation TLS cert.
// In addition, it is passed to homeserver containers to create TLS certs 
// for the homeservers
// This basically acts as a test only valid PKI.
func GetOrCreateCaCert() (*x509.Certificate, *rsa.PrivateKey, error) {
	var tlsCACertPath, tlsCAKeyPath string
	if os.Getenv("CI") == "true" {
		// When in CI we create the cert dir in the root directory instead.
		tlsCACertPath = path.Join("/ca", "ca.crt")
		tlsCAKeyPath = path.Join("/ca", "ca.key")
	} else {
		wd, err := os.Getwd()
		if err != nil {
			return nil, nil, err
		}
		tlsCACertPath = path.Join(wd, "ca", "ca.crt")
		tlsCAKeyPath = path.Join(wd,"ca", "ca.key")
		if _, err := os.Stat(path.Join(wd, "ca")); os.IsNotExist(err) {
			err = os.Mkdir(path.Join(wd, "ca"), 0770)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	if _, err := os.Stat(tlsCACertPath); err == nil {
		if _, err := os.Stat(tlsCAKeyPath); err == nil {
			// We already created a CA cert, let's use that.
			dat, err := ioutil.ReadFile(tlsCACertPath)
			if err != nil {
				return nil, nil, err
			}
			block, _ := pem.Decode([]byte(dat))
			if block == nil || block.Type != "CERTIFICATE" {
				return nil, nil, errors.New("ca.crt is not a valid pem encoded x509 cert")
			}
			caCerts, err := x509.ParseCertificates(block.Bytes)
			if err != nil {
				return nil, nil, err
			}
			if len(caCerts) != 1 {
				return nil, nil, errors.New("ca.crt contains none or more than one cert")
			}
			caCert := caCerts[0]
			dat, err = ioutil.ReadFile(tlsCAKeyPath)
			if err != nil {
				return nil, nil, err
			}
			block, _ = pem.Decode([]byte(dat))
			if block == nil || block.Type != "RSA PRIVATE KEY" {
				return nil, nil, errors.New("ca.key is not a valid pem encoded rsa private key")
			}
			priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
			if err != nil {
				return nil, nil, err
			}
			return caCert, priv, nil
		}
	}

	certificateDuration := time.Hour * 5
	priv, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, err
	}
	notBefore := time.Now()
	notAfter := notBefore.Add(certificateDuration)
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, nil, err
	}
	caCert := x509.Certificate{
		SerialNumber:          serialNumber,
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageCRLSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &caCert, &caCert, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, err
	}
	certOut, err := os.Create(tlsCACertPath)
	if err != nil {
		return nil, nil, err
	}
	
	defer certOut.Close() // nolint: errcheck
	if err = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return nil, nil, err
	}

	keyOut, err := os.OpenFile(tlsCAKeyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return nil, nil, err
	}
	defer keyOut.Close() // nolint: errcheck
	err = pem.Encode(keyOut, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	})
	if err != nil {
		return nil, nil, err
	}
	return &caCert, priv, nil
}

// federationServer creates a federation server with the given handler
func federationServer(name string, h http.Handler) (*http.Server, string, string, error) {
	var derBytes []byte
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
	if os.Getenv("COMPLEMENT_CA") == "true" {
		// Gate COMPLEMENT_CA
		ca, caPrivKey, err := GetOrCreateCaCert()
		if err != nil {
			return nil, "", "", err
		}
		derBytes, err = x509.CreateCertificate(rand.Reader, &template, ca, &priv.PublicKey, caPrivKey)
		if err != nil {
			return nil, "", "", err
		}
	} else {
		derBytes, err = x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
		if err != nil {
			return nil, "", "", err
		}
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
		if string(req.ServerName) == f.srv.ServerName && req.KeyID == f.srv.KeyID {
			publicKey := f.srv.Priv.Public().(ed25519.PublicKey)
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
