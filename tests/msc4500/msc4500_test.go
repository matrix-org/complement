package msc4500

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/tidwall/gjson"
	"github.com/zeebo/blake3"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

// TestMSC4500State exercises the MSC4500 state_accumulator endpoint and the
// outbound state_hashes extension on /send transactions.
func TestMSC4500State(t *testing.T) {
	t.Run("Accumulator", testMSC4500StateAccumulator)
	t.Run("HashMatch", testMSC4500StateHashMatch)
	t.Run("HashMismatch", testMSC4500StateHashMismatch)
	t.Run("Outbound", testMSC4500StateOutbound)
}

// testMSC4500StateAccumulator verifies that the state_accumulator endpoint
// returns a valid 2048-byte base64url encoded lattice and the matching BLAKE3-256 digest.
func testMSC4500StateAccumulator(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	// Create a remote homeserver to make authenticated federation requests
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(nil, nil),
	)
	cancel := srv.Listen()
	defer cancel()

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	charlie := srv.UserID("charlie")
	_ = srv.MustJoinRoom(t, deployment, "hs1", roomID, charlie)

	token := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

	// Get the last event ID from the sync
	res := alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "messages"}, client.WithQueries(url.Values{
		"dir":   {"b"},
		"limit": {"1"},
		"from":  {token},
	}))
	body := must.ParseJSON(t, res.Body)
	eventID := body.Get("chunk.0.event_id").Str

	must.NotEqual(t, eventID, "", "Failed to find event ID")

	// Call the federation endpoint using signed federation request from srv
	reqURI := fmt.Sprintf("/_matrix/federation/unstable/tk.nutra.msc4500/state_accumulator/%s?event_id=%s", roomID, eventID)
	req := fclient.NewFederationRequest("GET", srv.ServerName(), deployment.GetFullyQualifiedHomeserverName(t, "hs1"), reqURI)

	fedRes, err := srv.DoFederationRequest(context.Background(), t, deployment, req)
	must.NotError(t, "do federation request", err)
	defer fedRes.Body.Close()

	fedBody := must.ParseJSON(t, fedRes.Body)

	must.MatchGJSON(t, fedBody, match.JSONKeyEqual("event_id", eventID))
	must.MatchGJSON(t, fedBody, match.JSONKeyEqual("algorithm", "lthash16-v1"))

	latticeB64 := fedBody.Get("lattice").Str
	digestB64 := fedBody.Get("digest").Str

	must.NotEqual(t, latticeB64, "", "Lattice is empty")
	must.Equal(t, len(digestB64), 43, "Digest is not 43 base64url characters")

	// Verify the digest matches the lattice
	latticeBytes, err := base64.RawURLEncoding.DecodeString(latticeB64)
	must.NotError(t, "base64 decode", err)
	must.Equal(t, len(latticeBytes), 2048, "Lattice is not 2048 bytes")

	hash := blake3.Sum256(latticeBytes)
	expectedDigestB64 := base64.RawURLEncoding.EncodeToString(hash[:])

	must.Equal(t, digestB64, expectedDigestB64, "Digest does not match BLAKE3-256 of lattice")
}

func testMSC4500StateHashMatch(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	// Create a remote homeserver
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(nil, nil),
	)
	cancel := srv.Listen()
	defer cancel()

	// Alice creates a public room
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	charlie := srv.UserID("charlie")
	serverRoom := srv.MustJoinRoom(t, deployment, "hs1", roomID, charlie)
	joinEvent := serverRoom.CurrentState("m.room.member", charlie)
	must.NotEqual(t, joinEvent, nil, "expected charlie join event in remote room state")

	event := srv.MustCreateEvent(t, serverRoom, federation.Event{
		Sender: charlie,
		Type:   "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Matching state hash event",
		},
	})

	// The message event does not change room state, so the post-event digest is
	// the same as the one after charlie's join event, which hs1 already knows.
	digestHex := mustGetStateAccumulatorDigest(t, srv, deployment, roomID, joinEvent.EventID())

	pdus := []json.RawMessage{event.JSON()}
	txnJSON := map[string]interface{}{
		"origin":           srv.ServerName(),
		"origin_server_ts": time.Now().UnixNano() / 1000000,
		"pdus":             pdus,
		"tk.nutra.msc4500.state_hashes": map[string]interface{}{
			event.EventID(): map[string]interface{}{
				"algorithm": "lthash16-v1",
				"after":     digestHex,
			},
		},
	}

	txnBody, err := json.Marshal(txnJSON)
	must.NotError(t, "json marshal txn", err)

	txnID := fmt.Sprintf("txn-%d", time.Now().UnixNano())
	reqURI := fmt.Sprintf("/_matrix/federation/v1/send/%s", txnID)

	req := fclient.NewFederationRequest("PUT", srv.ServerName(), deployment.GetFullyQualifiedHomeserverName(t, "hs1"), reqURI)
	err = req.SetContent(json.RawMessage(txnBody))
	must.NotError(t, "set content", err)

	res, err := srv.DoFederationRequest(context.Background(), t, deployment, req)
	must.NotError(t, "do federation request", err)

	resBody, err := io.ReadAll(res.Body)
	must.NotError(t, "read res body", err)
	must.NotError(t, "close res body", res.Body.Close())

	t.Logf("Response: %s", string(resBody))

	// Verify the response does not contain state_hash_mismatch for the event
	parsedRes := gjson.ParseBytes(resBody)
	mismatchObj := gjson.Result{}
	parsedRes.Get("pdus").ForEach(func(key, value gjson.Result) bool {
		if key.Str == event.EventID() {
			mismatchObj = value.Get("state_hash_mismatch")
			return false
		}
		return true
	})
	must.Equal(t, mismatchObj.Exists(), false, "state_hash_mismatch should not be present in response")
}

func testMSC4500StateHashMismatch(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	// Create a remote homeserver
	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
		federation.HandleTransactionRequests(nil, nil),
	)
	cancel := srv.Listen()
	defer cancel()

	// Alice creates a public room
	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	charlie := srv.UserID("charlie")
	serverRoom := srv.MustJoinRoom(t, deployment, "hs1", roomID, charlie)

	badEvent := srv.MustCreateEvent(t, serverRoom, federation.Event{
		Sender: charlie,
		Type:   "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Bad state hash event",
		},
	})

	pdus := []json.RawMessage{badEvent.JSON()}
	txnJSON := map[string]interface{}{
		"origin":           srv.ServerName(),
		"origin_server_ts": time.Now().UnixNano() / 1000000,
		"pdus":             pdus,
		"tk.nutra.msc4500.state_hashes": map[string]interface{}{
			badEvent.EventID(): map[string]interface{}{
				"algorithm": "lthash16-v1",
				"after":     "ABEiM0RVZneImaq7zN3u_wARIjNEVWZ3iJmqu8zd7v8",
			},
		},
	}

	txnBody, err := json.Marshal(txnJSON)
	must.NotError(t, "json marshal txn", err)

	txnID := fmt.Sprintf("txn-%d", time.Now().UnixNano())
	reqURI := fmt.Sprintf("/_matrix/federation/v1/send/%s", txnID)

	req := fclient.NewFederationRequest("PUT", srv.ServerName(), deployment.GetFullyQualifiedHomeserverName(t, "hs1"), reqURI)
	err = req.SetContent(json.RawMessage(txnBody))
	must.NotError(t, "set content", err)

	res, err := srv.DoFederationRequest(context.Background(), t, deployment, req)
	must.NotError(t, "do federation request", err)

	resBody, err := io.ReadAll(res.Body)
	must.NotError(t, "read res body", err)
	must.NotError(t, "close res body", res.Body.Close())

	t.Logf("Response: %s", string(resBody))

	// Verify the response contains state_hash_mismatch for the event
	parsedRes := gjson.ParseBytes(resBody)
	mismatchObj := gjson.Result{}
	parsedRes.Get("pdus").ForEach(func(key, value gjson.Result) bool {
		if key.Str == badEvent.EventID() {
			mismatchObj = value.Get("state_hash_mismatch")
			return false
		}
		return true
	})
	must.Equal(t, mismatchObj.Exists(), true, "state_hash_mismatch not found in response")
	must.Equal(t, mismatchObj.Get("algorithm").Str, "lthash16-v1", "mismatch algorithm wrong")
	expectedDigest := mustGetStateAccumulatorDigest(t, srv, deployment, roomID, badEvent.EventID())
	must.Equal(t, mismatchObj.Get("digest").Str, expectedDigest, "mismatch digest wrong")
}

func mustGetStateAccumulatorDigest(
	t *testing.T,
	srv *federation.Server,
	deployment complement.Deployment,
	roomID string,
	eventID string,
) string {
	t.Helper()

	reqURI := fmt.Sprintf("/_matrix/federation/unstable/tk.nutra.msc4500/state_accumulator/%s?event_id=%s", roomID, eventID)
	req := fclient.NewFederationRequest("GET", srv.ServerName(), deployment.GetFullyQualifiedHomeserverName(t, "hs1"), reqURI)

	fedRes, err := srv.DoFederationRequest(context.Background(), t, deployment, req)
	must.NotError(t, "do federation request", err)
	defer fedRes.Body.Close()

	fedBody := must.ParseJSON(t, fedRes.Body)
	digestB64 := fedBody.Get("digest").Str
	must.NotEqual(t, digestB64, "", "Digest is empty")
	return digestB64
}

// testMSC4500StateOutbound verifies that outbound /send transactions carry the
// MSC4500 state_hashes extension, so that remote
// servers can validate state equivalence across the wire.
func testMSC4500StateOutbound(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	// Remote homeserver that captures raw /send transaction bodies. We parse the
	// raw body (rather than gomatrixserverlib.Transaction) because the custom
	// state_hashes field would otherwise be dropped.
	found := helpers.NewWaiter()
	var (
		mu             sync.Mutex
		observedAfter  string
		observedDigest bool
		expectedDigest string
	)

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
		federation.HandleMakeSendJoinRequests(),
	)
	srv.Mux().HandleFunc("/_matrix/federation/v1/send/{transactionID}", func(w http.ResponseWriter, req *http.Request) {
		defer func() {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return
			}
			// Check the transaction for the state_hashes extension after reading it.
			checkMSC4500Outbound(body, found, &mu, &observedAfter, &observedDigest)
		}()
		w.WriteHeader(200)
		w.Write([]byte(`{"pdus":{}}`))
	}).Methods("PUT")

	cancel := srv.Listen()
	defer cancel()

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	charlie := srv.UserID("charlie")
	serverRoom := srv.MustJoinRoom(t, deployment, "hs1", roomID, charlie)
	joinEvent := serverRoom.CurrentState("m.room.member", charlie)
	must.NotEqual(t, joinEvent, nil, "expected charlie join event in remote room state")
	expectedDigest = mustGetStateAccumulatorDigest(t, srv, deployment, roomID, joinEvent.EventID())

	// Trigger an outbound transaction by having alice send a message and syncing
	// until it is present (which forces the homeserver to forward it to charlie).
	alice.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "hello",
		},
	})

	found.Waitf(t, 30*time.Second, "timed out waiting for outbound state_hashes on /send")

	mu.Lock()
	defer mu.Unlock()
	must.Equal(t, observedDigest, true, "did not observe a valid outbound state_hashes entry")
	must.Equal(t, observedAfter, expectedDigest, "outbound state_hashes digest wrong")
}

// checkMSC4500Outbound inspects a raw /send transaction body and finishes the
// waiter if it carries a valid state_hashes extension.
//
// It parses the body twice: once into a gomatrixserverlib.Transaction to confirm
// the payload is a well-formed /send transaction (optional - ignored on failure),
// and once into a generic map so the custom state_hashes extension (which the
// strongly-typed Transaction drops) can be inspected.
func checkMSC4500Outbound(raw json.RawMessage, found *helpers.Waiter, mu *sync.Mutex, observedAfter *string, observedDigest *bool) {
	// Optional: verify the body also unmarshals as a standard Transaction. This
	// is not required for the state_hashes check, so a parse error is ignored.
	var txn gomatrixserverlib.Transaction
	_ = json.Unmarshal(raw, &txn)

	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		return
	}
	stateHashes, ok := body["state_hashes"]
	if !ok {
		stateHashes, ok = body["tk.nutra.msc4500.state_hashes"]
	}
	if !ok {
		return
	}
	sh, ok := stateHashes.(map[string]interface{})
	if !ok || len(sh) == 0 {
		return
	}
	for _, v := range sh {
		entry, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		algo, _ := entry["algorithm"].(string)
		after, _ := entry["after"].(string)
		if algo == "lthash16-v1" && len(after) == 43 {
			mu.Lock()
			*observedAfter = after
			*observedDigest = true
			mu.Unlock()
			found.Finish()
			return
		}
	}
}
