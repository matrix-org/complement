// package federation is an EXPERIMENTAL set of functions for creating mock federation servers, for testing over federation.
// It is marked as EXPERIMENTAL as the API may break without warning.
package federation

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"math/big"
	"net"
	"net/http"
	"os"
	"path"
	"sync"
	"time"

	"github.com/matrix-org/gomatrix"
	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/tidwall/sjson"

	"github.com/gorilla/mux"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/util"

	"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/internal/config"
)

// Subset of Deployment used in federation
type FederationDeployment interface {
	GetConfig() *config.Complement
	RoundTripper() http.RoundTripper
}

// EXPERIMENTAL
// Server represents a federation server
type Server struct {
	t ct.TestLike

	// Default: true
	UnexpectedRequestsAreErrors bool

	Priv       ed25519.PrivateKey
	KeyID      gomatrixserverlib.KeyID
	serverName string
	listening  bool

	certPath string
	keyPath  string
	mux      *mux.Router
	srv      *http.Server

	directoryHandlerSetup bool
	aliases               map[string]string
	rooms                 map[string]*ServerRoom
	keyRing               *gomatrixserverlib.KeyRing
}

// EXPERIMENTAL
// NewServer creates a new federation server with configured options.
func NewServer(t ct.TestLike, deployment FederationDeployment, opts ...func(*Server)) *Server {
	// generate signing key
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		ct.Fatalf(t, "federation.NewServer failed to generate ed25519 key: %s", err)
	}

	srv := &Server{
		t:     t,
		Priv:  priv,
		KeyID: gomatrixserverlib.KeyID(fmt.Sprintf("ed25519:complement_%x", pub)),
		mux:   mux.NewRouter(),
		// The server name will be updated when the caller calls Listen() to include the port number
		// of the HTTP server e.g "host.docker.internal:56353"
		serverName:                  deployment.GetConfig().HostnameRunningComplement,
		rooms:                       make(map[string]*ServerRoom),
		aliases:                     make(map[string]string),
		UnexpectedRequestsAreErrors: true,
	}
	fetcher := &basicKeyFetcher{
		KeyFetcher: &gomatrixserverlib.DirectKeyFetcher{
			Client: fclient.NewClient(
				fclient.WithTransport(deployment.RoundTripper()),
			),
			IsLocalServerName: func(s spec.ServerName) bool {
				return s == spec.ServerName(deployment.GetConfig().HostnameRunningComplement)
			},
			LocalPublicKey: []byte(pub),
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
			body, _ := ioutil.ReadAll(req.Body)
			ct.Errorf(t, "Server.UnexpectedRequestsAreErrors=true received unexpected request to server: %s %s\n%s", req.Method, req.URL.Path, string(body))
		} else {
			t.Logf("Server.UnexpectedRequestsAreErrors=false received unexpected request to server: %s %s - sending 404 which may cause the HS to backoff from Complement", req.Method, req.URL.Path)
		}
		w.WriteHeader(404)
		w.Write([]byte("complement: federation server is not listening for this path"))
	})

	// generate certs and an http.Server
	httpServer, certPath, keyPath, err := federationServer(deployment.GetConfig(), srv.mux)
	if err != nil {
		ct.Fatalf(t, "complement: unable to create federation server and certificates: %s", err.Error())
	}
	srv.certPath = certPath
	srv.keyPath = keyPath
	srv.srv = httpServer

	for _, opt := range opts {
		opt(srv)
	}
	return srv
}

// Return the server name of this federation server. Only valid AFTER calling Listen() - doing so
// before will produce an error.
//
// It is not supported to call ServerName() before Listen() because Listen() modifies the server name.
// Listen() will select a random OS-provided high-numbered port to listen on, which then needs to be
// retrofitted into the server name so containers know how to route to it.
func (s *Server) ServerName() string {
	if !s.listening {
		ct.Fatalf(s.t, "ServerName() called before Listen() - this is not supported because Listen() chooses a high-numbered port and thus changes the server name. Ensure you Listen() first!")
	}
	return s.serverName
}

// UserID returns the complete user ID for the given localpart
func (s *Server) UserID(localpart string) string {
	if !s.listening {
		ct.Fatalf(s.t, "UserID() called before Listen() - this is not supported because Listen() chooses a high-numbered port and thus changes the server name and thus changes the user ID. Ensure you Listen() first!")
	}
	return fmt.Sprintf("@%s:%s", localpart, s.serverName)
}

// MakeAliasMapping will create a mapping of room alias to room ID on this server. Returns the alias.
// If this is the first time calling this function, a directory lookup handler will be added to
// handle alias requests over federation.
func (s *Server) MakeAliasMapping(aliasLocalpart, roomID string) string {
	if !s.listening {
		ct.Fatalf(s.t, "MakeAliasMapping() called before Listen() - this is not supported because Listen() chooses a high-numbered port and thus changes the server name and thus changes the room alias. Ensure you Listen() first!")
	}
	alias := fmt.Sprintf("#%s:%s", aliasLocalpart, s.serverName)
	s.aliases[alias] = roomID
	HandleDirectoryLookups()(s)
	return alias
}

// MustMakeRoom will add a room to this server so it is accessible to other servers when prompted via federation.
// The `events` will be added to this room. Returns the created room.
func (s *Server) MustMakeRoom(t ct.TestLike, roomVer gomatrixserverlib.RoomVersion, events []Event) *ServerRoom {
	if !s.listening {
		ct.Fatalf(s.t, "MustMakeRoom() called before Listen() - this is not supported because Listen() chooses a high-numbered port and thus changes the server name and thus changes the room ID. Ensure you Listen() first!")
	}
	// Generate a unique room ID, prefixed with an incrementing counter.
	// This ensures that room IDs are not re-used across tests, even if a Complement server happens
	// to re-use the same port as a previous one, which
	//  * reduces noise when searching through logs and
	//  * prevents homeservers from getting confused when multiple test cases re-use the same homeserver deployment.
	roomID := fmt.Sprintf("!%d-%s:%s", len(s.rooms), util.RandomString(18), s.serverName)
	t.Logf("Creating room %s with version %s", roomID, roomVer)
	room := NewServerRoom(roomVer, roomID)

	// sign all these events
	for _, ev := range events {
		signedEvent := s.MustCreateEvent(t, room, ev)
		room.AddEvent(signedEvent)
	}
	s.rooms[roomID] = room
	return room
}

// FederationClient returns a client which will sign requests using this server's key.
//
// The requests will be routed according to the deployment map in `deployment`, which satisfies the RoundTripper interface.
func (s *Server) FederationClient(deployment FederationDeployment) fclient.FederationClient {
	if !s.listening {
		ct.Fatalf(s.t, "FederationClient() called before Listen() - this is not supported because Listen() chooses a high-numbered port and thus changes the server name and thus changes the way federation requests are signed. Ensure you Listen() first!")
	}
	identity := fclient.SigningIdentity{
		ServerName: spec.ServerName(s.ServerName()),
		KeyID:      s.KeyID,
		PrivateKey: s.Priv,
	}
	f := fclient.NewFederationClient(
		[]*fclient.SigningIdentity{&identity},
		fclient.WithTransport(deployment.RoundTripper()),
	)
	return f
}

// MustSendTransaction sends the given PDUs/EDUs to the target destination, returning an error if the /send fails or if the response contains an error
// for any sent PDUs. Times out after 10 seconds.
func (s *Server) MustSendTransaction(t ct.TestLike, deployment FederationDeployment, destination string, pdus []json.RawMessage, edus []gomatrixserverlib.EDU) {
	t.Helper()
	cli := s.FederationClient(deployment)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	resp, err := cli.SendTransaction(ctx, gomatrixserverlib.Transaction{
		TransactionID: gomatrixserverlib.TransactionID(fmt.Sprintf("complement-%d", time.Now().Nanosecond())),
		Origin:        spec.ServerName(s.ServerName()),
		Destination:   spec.ServerName(destination),
		PDUs:          pdus,
		EDUs:          edus,
	})
	if err != nil {
		ct.Fatalf(t, "MustSendTransaction: %s", err)
	}
	for eventID, e := range resp.PDUs {
		if e.Error != "" {
			ct.Fatalf(t, "MustSendTransaction: response for %s contained error: %s", eventID, e.Error)
		}
	}
}

// SendFederationRequest signs and sends an arbitrary federation request from this server.
//
// The requests will be routed according to the deployment map in `deployment`.
func (s *Server) SendFederationRequest(
	ctx context.Context,
	t ct.TestLike,
	deployment FederationDeployment,
	req fclient.FederationRequest,
	resBody interface{},
) error {
	if err := req.Sign(spec.ServerName(s.serverName), s.KeyID, s.Priv); err != nil {
		return err
	}

	httpReq, err := req.HTTPRequest()
	if err != nil {
		return err
	}

	httpClient := fclient.NewClient(fclient.WithTransport(deployment.RoundTripper()))
	start := time.Now()
	err = httpClient.DoRequestAndParseResponse(ctx, httpReq, resBody)

	if httpError, ok := err.(gomatrix.HTTPError); ok {
		t.Logf("[SSAPI] %s %s%s => error(%d): %s (%s)", req.Method(), req.Destination(), req.RequestURI(), httpError.Code, err, time.Since(start))
	} else if err == nil {
		t.Logf("[SSAPI] %s %s%s => 2xx (%s)", req.Method(), req.Destination(), req.RequestURI(), time.Since(start))
	}
	return err
}

// DoFederationRequest signs and sends an arbitrary federation request from this server, and returns the response.
//
// The requests will be routed according to the deployment map in `deployment`.
func (s *Server) DoFederationRequest(
	ctx context.Context,
	t ct.TestLike,
	deployment FederationDeployment,
	req fclient.FederationRequest) (*http.Response, error) {
	if err := req.Sign(spec.ServerName(s.serverName), s.KeyID, s.Priv); err != nil {
		return nil, err
	}

	httpReq, err := req.HTTPRequest()
	if err != nil {
		return nil, err
	}

	httpClient := fclient.NewClient(fclient.WithTransport(deployment.RoundTripper()))
	start := time.Now()

	var resp *http.Response
	resp, err = httpClient.DoHTTPRequest(ctx, httpReq)

	if httpError, ok := err.(gomatrix.HTTPError); ok {
		t.Logf("[SSAPI] %s %s%s => error(%d): %s (%s)", req.Method(), req.Destination(), req.RequestURI(), httpError.Code, err, time.Since(start))
	} else if err == nil {
		t.Logf("[SSAPI] %s %s%s => %d (%s)", req.Method(), req.Destination(), req.RequestURI(), resp.StatusCode, time.Since(start))
	}
	return resp, err
}

// MustCreateEvent will create and sign a new latest event for the given room.
// It does not insert this event into the room however. See ServerRoom.AddEvent for that.
func (s *Server) MustCreateEvent(t ct.TestLike, room *ServerRoom, ev Event) gomatrixserverlib.PDU {
	t.Helper()
	proto, err := room.ProtoEventCreator(room, ev)
	if err != nil {
		ct.Fatalf(t, "MustCreateEvent: failed to create proto event: %v", err)
	}
	pdu, err := room.EventCreator(s, room, proto)
	if err != nil {
		ct.Fatalf(t, "MustCreateEvent: failed to create PDU: %v", err)
	}
	return pdu
}

// MustJoinRoom will make the server send a make_join and a send_join to join a room
// It returns the resultant room.
func (s *Server) MustJoinRoom(t ct.TestLike, deployment FederationDeployment, remoteServer spec.ServerName, roomID string, userID string, partialState ...bool) *ServerRoom {
	t.Helper()
	origin := spec.ServerName(s.serverName)
	fedClient := s.FederationClient(deployment)
	makeJoinResp, err := fedClient.MakeJoin(context.Background(), origin, remoteServer, roomID, userID)
	if err != nil {
		ct.Fatalf(t, "MustJoinRoom: make_join failed: %v", err)
	}
	roomVer := makeJoinResp.RoomVersion
	verImpl, err := gomatrixserverlib.GetRoomVersion(makeJoinResp.RoomVersion)
	if err != nil {
		ct.Fatalf(t, "MustJoinRoom: invalid room version: %v", err)
	}

	var senderID spec.SenderID
	signingKey := s.Priv
	keyID := s.KeyID
	origOrigin := origin
	switch roomVer {
	case gomatrixserverlib.RoomVersionPseudoIDs:
		_, key, err := ed25519.GenerateKey(nil)
		if err != nil {
			ct.Fatalf(t, "MustJoinRoom: failed generating senderID: %v", err)
		}
		senderID, signingKey, err = spec.SenderIDFromPseudoIDKey(key), key, nil
		keyID = "ed25519:1"
		origin = spec.ServerName(senderID)
		mapping := gomatrixserverlib.MXIDMapping{
			UserRoomKey: senderID,
			UserID:      userID,
		}
		if err = mapping.Sign(origOrigin, s.KeyID, s.Priv); err != nil {
			ct.Fatalf(t, "MustJoinRoom: failed signing mxid_mapping: %v", err)
		}

		path := "mxid_mapping"
		eventJSON, err := sjson.SetBytes(makeJoinResp.JoinEvent.Content, path, mapping)
		if err != nil {
			ct.Fatalf(t, "MustJoinRoom: failed setting mxid_mapping in content: %v", err)
		}
		eventJSON = gomatrixserverlib.CanonicalJSONAssumeValid(eventJSON)
		makeJoinResp.JoinEvent.Content = eventJSON
	default:
		senderID = spec.SenderID(userID)
	}

	stateKey := string(senderID)
	makeJoinResp.JoinEvent.SenderID = string(senderID)
	makeJoinResp.JoinEvent.StateKey = &stateKey

	eb := verImpl.NewEventBuilderFromProtoEvent(&makeJoinResp.JoinEvent)
	joinEvent, err := eb.Build(time.Now(), origin, keyID, signingKey)
	if err != nil {
		ct.Fatalf(t, "MustJoinRoom: failed to sign event: %v", err)
	}
	var sendJoinResp fclient.RespSendJoin
	if len(partialState) == 0 || !partialState[0] {
		// Default to doing a regular join.
		sendJoinResp, err = fedClient.SendJoin(context.Background(), origOrigin, remoteServer, joinEvent)
	} else {
		sendJoinResp, err = fedClient.SendJoinPartialState(context.Background(), origOrigin, remoteServer, joinEvent)
	}
	if err != nil {
		ct.Fatalf(t, "MustJoinRoom: send_join failed: %v", err)
	}
	stateEvents := sendJoinResp.StateEvents.UntrustedEvents(roomVer)
	room := NewServerRoom(roomVer, roomID)
	for _, ev := range stateEvents {
		room.ReplaceCurrentState(ev)
	}
	room.AddEvent(joinEvent)
	s.rooms[roomID] = room

	t.Logf("Server.MustJoinRoom joined room ID %s", roomID)

	return room
}

// Leaves a room. If this is rejecting an invite then a make_leave request is made first, before send_leave.
func (s *Server) MustLeaveRoom(t ct.TestLike, deployment FederationDeployment, remoteServer spec.ServerName, roomID string, userID string) {
	t.Helper()
	origin := spec.ServerName(s.serverName)
	fedClient := s.FederationClient(deployment)
	var leaveEvent gomatrixserverlib.PDU
	room := s.rooms[roomID]
	if room == nil {
		// e.g rejecting an invite
		makeLeaveResp, err := fedClient.MakeLeave(context.Background(), origin, remoteServer, roomID, userID)
		if err != nil {
			ct.Fatalf(t, "MustLeaveRoom: (rejecting invite) make_leave failed: %v", err)
		}
		verImpl, err := gomatrixserverlib.GetRoomVersion(makeLeaveResp.RoomVersion)
		if err != nil {
			ct.Fatalf(t, "MustLeaveRoom: invalid room version: %v", err)
		}
		eb := verImpl.NewEventBuilderFromProtoEvent(&makeLeaveResp.LeaveEvent)
		leaveEvent, err = eb.Build(time.Now(), origin, s.KeyID, s.Priv)
		if err != nil {
			ct.Fatalf(t, "MustLeaveRoom: (rejecting invite) failed to sign event: %v", err)
		}
	} else {
		// make the leave event
		leaveEvent = s.MustCreateEvent(t, room, Event{
			Type:     "m.room.member",
			StateKey: &userID,
			Sender:   userID,
			Content: map[string]interface{}{
				"membership": "leave",
			},
		})
	}
	err := fedClient.SendLeave(context.Background(), origin, remoteServer, leaveEvent)
	if err != nil {
		ct.Fatalf(t, "MustLeaveRoom: send_leave failed: %v", err)
	}
	room.AddEvent(leaveEvent)
	s.rooms[roomID] = room

	t.Logf("Server.MustLeaveRoom left room ID %s", roomID)
}

// AddRoom is a low-level function to add a custom room to the server. Useful to mix custom logic with helper functions.
func (s *Server) AddRoom(room *ServerRoom) {
	s.rooms[room.RoomID] = room
}

// ValidFederationRequest is a wrapper around http.HandlerFunc which automatically validates the incoming
// federation request and supports sending back JSON. Fails the test if the request is not valid.
func (s *Server) ValidFederationRequest(t ct.TestLike, handler func(fr *fclient.FederationRequest, pathParams map[string]string) util.JSONResponse) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		// Check federation signature
		fedReq, errResp := fclient.VerifyHTTPRequest(
			req, time.Now(), spec.ServerName(s.serverName), nil, s.keyRing,
		)
		if fedReq == nil {
			ct.Errorf(t,
				"complement: ValidFederationRequest: HTTP Code %d. Invalid http request: %s",
				errResp.Code, errResp.JSON,
			)

			w.WriteHeader(errResp.Code)
			b, _ := json.Marshal(errResp.JSON)
			w.Write(b)
			return
		}
		vars := mux.Vars(req)

		// invoke the handler
		res := handler(fedReq, vars)

		// send back the response
		for k, v := range res.Headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(res.Code)
		b, _ := json.Marshal(res.JSON)
		w.Write(b)
	}
}

// Mux returns this server's router so you can attach additional paths.
func (s *Server) Mux() *mux.Router {
	return s.mux
}

// Listen for federation server requests - call the returned function to gracefully close the server.
func (s *Server) Listen() (cancel func()) {
	if s.listening {
		return
	}
	var wg sync.WaitGroup
	wg.Add(1)

	ln, err := net.Listen("tcp", ":0") //nolint
	if err != nil {
		ct.Fatalf(s.t, "ListenFederationServer: net.Listen failed: %s", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	s.serverName += fmt.Sprintf(":%d", port)
	s.listening = true

	go func() {
		defer ln.Close()
		defer wg.Done()
		err := s.srv.ServeTLS(ln, s.certPath, s.keyPath)
		if err != nil && err != http.ErrServerClosed {
			s.t.Logf("ListenFederationServer: ServeTLS failed: %s", err)
			// Note that running s.t.FailNow is not allowed in a separate goroutine
			// Tests will likely fail if the server is not listening anyways
		}
	}()

	return func() {
		err := s.srv.Close()
		if err != nil {
			ct.Fatalf(s.t, "ListenFederationServer: failed to shutdown server: %s", err)
		}
		wg.Wait() // wait for the server to shutdown
	}
}

// federationServer creates a federation server with the given handler
func federationServer(cfg *config.Complement, h http.Handler) (*http.Server, string, string, error) {
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
		Subject: pkix.Name{
			Organization:  []string{"matrix.org"},
			Country:       []string{"GB"},
			Province:      []string{"London"},
			Locality:      []string{"London"},
			StreetAddress: []string{"123 Street"},
			PostalCode:    []string{"12345"},
			CommonName:    cfg.HostnameRunningComplement,
		},
	}
	host := cfg.HostnameRunningComplement
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = append(template.IPAddresses, ip)
	} else {
		template.DNSNames = append(template.DNSNames, host)
	}

	// derive a new certificate from the base complement one
	derBytes, err = x509.CreateCertificate(rand.Reader, &template, cfg.CACertificate, &priv.PublicKey, cfg.CAPrivateKey)
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

type nopKeyDatabase struct {
	gomatrixserverlib.KeyFetcher
}

func (d *nopKeyDatabase) StoreKeys(ctx context.Context, results map[gomatrixserverlib.PublicKeyLookupRequest]gomatrixserverlib.PublicKeyLookupResult) error {
	return nil
}
func (f *nopKeyDatabase) FetchKeys(
	ctx context.Context,
	requests map[gomatrixserverlib.PublicKeyLookupRequest]spec.Timestamp) (
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
	requests map[gomatrixserverlib.PublicKeyLookupRequest]spec.Timestamp) (
	map[gomatrixserverlib.PublicKeyLookupRequest]gomatrixserverlib.PublicKeyLookupResult, error,
) {
	result := make(map[gomatrixserverlib.PublicKeyLookupRequest]gomatrixserverlib.PublicKeyLookupResult, len(requests))
	for req := range requests {
		if string(req.ServerName) == f.srv.serverName && req.KeyID == f.srv.KeyID {
			publicKey := f.srv.Priv.Public().(ed25519.PublicKey)
			result[req] = gomatrixserverlib.PublicKeyLookupResult{
				ValidUntilTS: spec.AsTimestamp(time.Now().Add(24 * time.Hour)),
				ExpiredTS:    gomatrixserverlib.PublicKeyNotExpired,
				VerifyKey: gomatrixserverlib.VerifyKey{
					Key: spec.Base64Bytes(publicKey),
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

// SupportedRoomVersions is a convenience method which returns a list of the room versions supported by gomatrixserverlib.
func SupportedRoomVersions() []gomatrixserverlib.RoomVersion {
	supportedRoomVersions := make([]gomatrixserverlib.RoomVersion, 0, 10)
	for v := range gomatrixserverlib.StableRoomVersions() {
		supportedRoomVersions = append(supportedRoomVersions, v)
	}
	return supportedRoomVersions
}
