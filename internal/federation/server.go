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
	"io/ioutil"
	"math/big"
	"net/http"
	"os"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
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

// HandleKeyRequests will process GET /_matrix/key/v2/server requests
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
