package client

import (
	"bytes"
	"context" // nolint:gosec
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/matrix-org/gomatrixserverlib"
	"github.com/tidwall/gjson"
	"maunium.net/go/mautrix/crypto/olm"

	"github.com/matrix-org/complement/b"
)

// TestLike is an interface that testing.T satisfies. All client functions accept a TestLike interface,
// with the intention of a `testing.T` being passed into them. However, the client may be used in non-test
// scenarios e.g benchmarks, which can then use the same client by just implementing this interface.
type TestLike interface {
	Helper()
	Logf(msg string, args ...interface{})
	Skipf(msg string, args ...interface{})
	Error(args ...interface{})
	Errorf(msg string, args ...interface{})
	Fatalf(msg string, args ...interface{})
}

const ansiRedForeground = "\x1b[31m"
const ansiResetForeground = "\x1b[39m"

// errorf is a wrapper around t.Errorf which prints the failing error message in red.
func errorf(t TestLike, format string, args ...any) {
	t.Helper()
	format = ansiRedForeground + format + ansiResetForeground
	t.Errorf(format, args...)
}

// fatalf is a wrapper around t.Fatalf which prints the failing error message in red.
func fatalf(t TestLike, format string, args ...any) {
	t.Helper()
	format = ansiRedForeground + format + ansiResetForeground
	t.Fatalf(format, args...)
}

type ctxKey string

const (
	CtxKeyWithRetryUntil ctxKey = "complement_retry_until" // contains *retryUntilParams
)

type retryUntilParams struct {
	timeout time.Duration
	untilFn func(*http.Response) bool
}

// RequestOpt is a functional option which will modify an outgoing HTTP request.
// See functions starting with `With...` in this package for more info.
type RequestOpt func(req *http.Request)

type CSAPI struct {
	UserID      string
	AccessToken string
	DeviceID    string
	BaseURL     string
	Client      *http.Client
	// how long are we willing to wait for MustSyncUntil.... calls
	SyncUntilTimeout time.Duration
	// True to enable verbose logging
	Debug bool

	txnID int64
}

// CreateMedia creates an MXC URI for asynchronous media uploads.
func (c *CSAPI) CreateMedia(t TestLike) string {
	t.Helper()
	res := c.MustDo(t, "POST", []string{"_matrix", "media", "v1", "create"})
	body := ParseJSON(t, res)
	return GetJSONFieldStr(t, body, "content_uri")
}

// UploadMediaAsync uploads the provided content to the given server and media ID. Fails the test on error.
func (c *CSAPI) UploadMediaAsync(t TestLike, serverName, mediaID string, fileBody []byte, fileName string, contentType string) {
	t.Helper()
	query := url.Values{}
	if fileName != "" {
		query.Set("filename", fileName)
	}
	c.MustDo(
		t, "PUT", []string{"_matrix", "media", "v3", "upload", serverName, mediaID},
		WithRawBody(fileBody), WithContentType(contentType), WithQueries(query),
	)
}

// UploadContent uploads the provided content with an optional file name. Fails the test on error. Returns the MXC URI.
func (c *CSAPI) UploadContent(t TestLike, fileBody []byte, fileName string, contentType string) string {
	t.Helper()
	query := url.Values{}
	if fileName != "" {
		query.Set("filename", fileName)
	}
	res := c.MustDo(
		t, "POST", []string{"_matrix", "media", "v3", "upload"},
		WithRawBody(fileBody), WithContentType(contentType), WithQueries(query),
	)
	body := ParseJSON(t, res)
	return GetJSONFieldStr(t, body, "content_uri")
}

// DownloadContent downloads media from the server, returning the raw bytes and the Content-Type. Fails the test on error.
func (c *CSAPI) DownloadContent(t TestLike, mxcUri string) ([]byte, string) {
	t.Helper()
	origin, mediaId := SplitMxc(mxcUri)
	res := c.MustDo(t, "GET", []string{"_matrix", "media", "v3", "download", origin, mediaId})
	contentType := res.Header.Get("Content-Type")
	b, err := io.ReadAll(res.Body)
	if err != nil {
		errorf(t, err.Error())
	}
	return b, contentType
}

// MustCreateRoom creates a room with an optional HTTP request body. Fails the test on error. Returns the room ID.
func (c *CSAPI) MustCreateRoom(t TestLike, reqBody map[string]interface{}) string {
	t.Helper()
	res := c.CreateRoom(t, reqBody)
	mustRespond2xx(t, res)
	resBody := ParseJSON(t, res)
	return GetJSONFieldStr(t, resBody, "room_id")
}

// CreateRoom creates a room with an optional HTTP request body.
func (c *CSAPI) CreateRoom(t TestLike, body map[string]interface{}) *http.Response {
	t.Helper()
	return c.Do(t, "POST", []string{"_matrix", "client", "v3", "createRoom"}, WithJSONBody(t, body))
}

// MustJoinRoom joins the room ID or alias given, else fails the test. Returns the room ID.
func (c *CSAPI) MustJoinRoom(t TestLike, roomIDOrAlias string, serverNames []string) string {
	t.Helper()
	res := c.JoinRoom(t, roomIDOrAlias, serverNames)
	mustRespond2xx(t, res)
	// return the room ID if we joined with it
	if roomIDOrAlias[0] == '!' {
		return roomIDOrAlias
	}
	// otherwise we should be told the room ID if we joined via an alias
	body := ParseJSON(t, res)
	return GetJSONFieldStr(t, body, "room_id")
}

// JoinRoom joins the room ID or alias given. Returns the raw http response
func (c *CSAPI) JoinRoom(t TestLike, roomIDOrAlias string, serverNames []string) *http.Response {
	t.Helper()
	// construct URL query parameters
	query := make(url.Values, len(serverNames))
	for _, serverName := range serverNames {
		query.Add("server_name", serverName)
	}
	// join the room
	return c.Do(
		t, "POST", []string{"_matrix", "client", "v3", "join", roomIDOrAlias},
		WithQueries(query), WithJSONBody(t, map[string]interface{}{}),
	)
}

// MustLeaveRoom leaves the room ID, else fails the test.
func (c *CSAPI) MustLeaveRoom(t TestLike, roomID string) {
	res := c.LeaveRoom(t, roomID)
	mustRespond2xx(t, res)
}

// LeaveRoom leaves the room ID.
func (c *CSAPI) LeaveRoom(t TestLike, roomID string) *http.Response {
	t.Helper()
	// leave the room
	body := map[string]interface{}{}
	return c.Do(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "leave"}, WithJSONBody(t, body))
}

// InviteRoom invites userID to the room ID, else fails the test.
func (c *CSAPI) MustInviteRoom(t TestLike, roomID string, userID string) {
	t.Helper()
	res := c.InviteRoom(t, roomID, userID)
	mustRespond2xx(t, res)
}

// InviteRoom invites userID to the room ID, else fails the test.
func (c *CSAPI) InviteRoom(t TestLike, roomID string, userID string) *http.Response {
	t.Helper()
	// Invite the user to the room
	body := map[string]interface{}{
		"user_id": userID,
	}
	return c.Do(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "invite"}, WithJSONBody(t, body))
}

func (c *CSAPI) MustGetGlobalAccountData(t TestLike, eventType string) *http.Response {
	res := c.GetGlobalAccountData(t, eventType)
	mustRespond2xx(t, res)
	return res
}

func (c *CSAPI) GetGlobalAccountData(t TestLike, eventType string) *http.Response {
	return c.Do(t, "GET", []string{"_matrix", "client", "v3", "user", c.UserID, "account_data", eventType})
}

func (c *CSAPI) MustSetGlobalAccountData(t TestLike, eventType string, content map[string]interface{}) *http.Response {
	return c.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "user", c.UserID, "account_data", eventType}, WithJSONBody(t, content))
}

func (c *CSAPI) MustGetRoomAccountData(t TestLike, roomID string, eventType string) *http.Response {
	res := c.GetRoomAccountData(t, roomID, eventType)
	mustRespond2xx(t, res)
	return res
}

func (c *CSAPI) GetRoomAccountData(t TestLike, roomID string, eventType string) *http.Response {
	return c.Do(t, "GET", []string{"_matrix", "client", "v3", "user", c.UserID, "rooms", roomID, "account_data", eventType})
}

func (c *CSAPI) MustSetRoomAccountData(t TestLike, roomID string, eventType string, content map[string]interface{}) *http.Response {
	return c.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "user", c.UserID, "rooms", roomID, "account_data", eventType}, WithJSONBody(t, content))
}

// GetAllPushRules fetches all configured push rules for a user from the homeserver.
// Push rules are returned as a parsed gjson result
//
// Example of printing the IDs of all underride rules of the current user:
//
//	allPushRules := c.GetAllPushRules(t)
//	globalUnderridePushRules := allPushRules.Get("global").Get("underride").Array()
//
//	for index, rule := range globalUnderridePushRules {
//	  fmt.Printf("This rule's ID is: %s\n", rule.Get("rule_id").Str)
//	}
//
// Push rules are returned in the same order received from the homeserver.
func (c *CSAPI) GetAllPushRules(t TestLike) gjson.Result {
	t.Helper()

	// We have to supply an empty string to the end of this path in order to generate a trailing slash.
	// See https://github.com/matrix-org/matrix-spec/issues/457
	res := c.MustDo(t, "GET", []string{"_matrix", "client", "v3", "pushrules", ""})
	pushRulesBytes := ParseJSON(t, res)
	return gjson.ParseBytes(pushRulesBytes)
}

// GetPushRule queries the contents of a client's push rule by scope, kind and rule ID.
// A parsed gjson result is returned. Fails the test if the query to server returns a non-2xx status code.
//
// Example of checking that a global underride rule contains the expected actions:
//
//	containsDisplayNameRule := c.GetPushRule(t, "global", "underride", ".m.rule.contains_display_name")
//	must.MatchGJSON(
//	  t,
//	  containsDisplayNameRule,
//	  match.JSONKeyEqual("actions", []interface{}{
//	    "notify",
//	    map[string]interface{}{"set_tweak": "sound", "value": "default"},
//	    map[string]interface{}{"set_tweak": "highlight"},
//	  }),
//	)
func (c *CSAPI) GetPushRule(t TestLike, scope string, kind string, ruleID string) gjson.Result {
	t.Helper()

	res := c.MustDo(t, "GET", []string{"_matrix", "client", "v3", "pushrules", scope, kind, ruleID})
	pushRuleBytes := ParseJSON(t, res)
	return gjson.ParseBytes(pushRuleBytes)
}

// SetPushRule creates a new push rule on the user, or modifies an existing one.
// If `before` or `after` parameters are not set to an empty string, their values
// will be set as the `before` and `after` query parameters respectively on the
// "set push rules" client endpoint:
// https://spec.matrix.org/v1.5/client-server-api/#put_matrixclientv3pushrulesscopekindruleid
//
// Example of setting a push rule with ID 'com.example.rule2' that must come after 'com.example.rule1':
//
//	c.SetPushRule(t, "global", "underride", "com.example.rule2", map[string]interface{}{
//	  "actions": []string{"dont_notify"},
//	}, nil, "com.example.rule1")
func (c *CSAPI) SetPushRule(t TestLike, scope string, kind string, ruleID string, body map[string]interface{}, before string, after string) *http.Response {
	t.Helper()

	// If the `before` or `after` arguments have been provided, construct same-named query parameters
	queryParams := url.Values{}
	if before != "" {
		queryParams.Add("before", before)
	}
	if after != "" {
		queryParams.Add("after", after)
	}

	return c.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "pushrules", scope, kind, ruleID}, WithJSONBody(t, body), WithQueries(queryParams))
}

// Unsafe_SendEventUnsynced sends `e` into the room. This function is UNSAFE as it does not wait
// for the event to be fully processed. This can cause flakey tests. Prefer `SendEventSynced`.
// Returns the event ID of the sent event.
func (c *CSAPI) Unsafe_SendEventUnsynced(t TestLike, roomID string, e b.Event) string {
	t.Helper()
	txnID := int(atomic.AddInt64(&c.txnID, 1))
	return c.Unsafe_SendEventUnsyncedWithTxnID(t, roomID, e, strconv.Itoa(txnID))
}

// SendEventUnsyncedWithTxnID sends `e` into the room with a prescribed transaction ID.
// This is useful for writing tests that interrogate transaction semantics. This function is UNSAFE
// as it does not wait for the event to be fully processed. This can cause flakey tests. Prefer `SendEventSynced`.
// Returns the event ID of the sent event.
func (c *CSAPI) Unsafe_SendEventUnsyncedWithTxnID(t TestLike, roomID string, e b.Event, txnID string) string {
	t.Helper()
	paths := []string{"_matrix", "client", "v3", "rooms", roomID, "send", e.Type, txnID}
	if e.StateKey != nil {
		paths = []string{"_matrix", "client", "v3", "rooms", roomID, "state", e.Type, *e.StateKey}
	}
	if e.Sender != "" && e.Sender != c.UserID {
		fatalf(t, "Event.Sender must not be set, as this is set by the client in use (%s)", c.UserID)
	}
	res := c.MustDo(t, "PUT", paths, WithJSONBody(t, e.Content))
	body := ParseJSON(t, res)
	eventID := GetJSONFieldStr(t, body, "event_id")
	return eventID
}

// SendEventSynced sends `e` into the room and waits for its event ID to come down /sync.
// Returns the event ID of the sent event.
func (c *CSAPI) SendEventSynced(t TestLike, roomID string, e b.Event) string {
	t.Helper()
	eventID := c.Unsafe_SendEventUnsynced(t, roomID, e)
	t.Logf("SendEventSynced waiting for event ID %s", eventID)
	c.MustSyncUntil(t, SyncReq{}, SyncTimelineHas(roomID, func(r gjson.Result) bool {
		return r.Get("event_id").Str == eventID
	}))
	return eventID
}

// SendRedaction sends a redaction request. Will fail if the returned HTTP request code is not 200. Returns the
// event ID of the redaction event.
func (c *CSAPI) MustSendRedaction(t TestLike, roomID string, content map[string]interface{}, eventID string) string {
	res := c.SendRedaction(t, roomID, content, eventID)
	mustRespond2xx(t, res)
	body := ParseJSON(t, res)
	return GetJSONFieldStr(t, body, "event_id")
}

// SendRedaction sends a redaction request.
func (c *CSAPI) SendRedaction(t TestLike, roomID string, content map[string]interface{}, eventID string) *http.Response {
	t.Helper()
	txnID := int(atomic.AddInt64(&c.txnID, 1))
	paths := []string{"_matrix", "client", "v3", "rooms", roomID, "redact", eventID, strconv.Itoa(txnID)}
	return c.Do(t, "PUT", paths, WithJSONBody(t, content))
}

// MustSendTyping marks this user as typing until the timeout is reached. If isTyping is false, timeout is ignored.
func (c *CSAPI) MustSendTyping(t TestLike, roomID string, isTyping bool, timeoutMillis int) {
	res := c.SendTyping(t, roomID, isTyping, timeoutMillis)
	mustRespond2xx(t, res)
}

// SendTyping marks this user as typing until the timeout is reached. If isTyping is false, timeout is ignored.
func (c *CSAPI) SendTyping(t TestLike, roomID string, isTyping bool, timeoutMillis int) *http.Response {
	content := map[string]interface{}{
		"typing": isTyping,
	}
	if isTyping {
		content["timeout"] = timeoutMillis
	}
	return c.Do(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "typing", c.UserID}, WithJSONBody(t, content))
}

// GetCapbabilities queries the server's capabilities
func (c *CSAPI) GetCapabilities(t TestLike) []byte {
	t.Helper()
	res := c.MustDo(t, "GET", []string{"_matrix", "client", "v3", "capabilities"})
	body, err := io.ReadAll(res.Body)
	if err != nil {
		fatalf(t, "unable to read response body: %v", err)
	}
	return body
}

// GetDefaultRoomVersion returns the server's default room version
func (c *CSAPI) GetDefaultRoomVersion(t TestLike) gomatrixserverlib.RoomVersion {
	t.Helper()
	capabilities := c.GetCapabilities(t)
	defaultVersion := gjson.GetBytes(capabilities, `capabilities.m\.room_versions.default`)
	if !defaultVersion.Exists() {
		// spec says use RoomV1 in this case
		return gomatrixserverlib.RoomVersionV1
	}

	return gomatrixserverlib.RoomVersion(defaultVersion.Str)
}

func (c *CSAPI) MustGenerateOneTimeKeys(t TestLike, otkCount uint) (deviceKeys map[string]interface{}, oneTimeKeys map[string]interface{}) {
	t.Helper()
	account := olm.NewAccount()
	ed25519Key, curveKey := account.IdentityKeys()

	ed25519KeyID := fmt.Sprintf("ed25519:%s", c.DeviceID)
	curveKeyID := fmt.Sprintf("curve25519:%s", c.DeviceID)

	deviceKeys = map[string]interface{}{
		"user_id":    c.UserID,
		"device_id":  c.DeviceID,
		"algorithms": []interface{}{"m.olm.v1.curve25519-aes-sha2", "m.megolm.v1.aes-sha2"},
		"keys": map[string]interface{}{
			ed25519KeyID: ed25519Key.String(),
			curveKeyID:   curveKey.String(),
		},
	}

	signature, _ := account.SignJSON(deviceKeys)

	deviceKeys["signatures"] = map[string]interface{}{
		c.UserID: map[string]interface{}{
			ed25519KeyID: signature,
		},
	}

	account.GenOneTimeKeys(otkCount)
	oneTimeKeys = map[string]interface{}{}

	for kid, key := range account.OneTimeKeys() {
		keyID := fmt.Sprintf("signed_curve25519:%s", kid)
		keyMap := map[string]interface{}{
			"key": key.String(),
		}

		signature, _ = account.SignJSON(keyMap)

		keyMap["signatures"] = map[string]interface{}{
			c.UserID: map[string]interface{}{
				ed25519KeyID: signature,
			},
		}

		oneTimeKeys[keyID] = keyMap
	}
	return deviceKeys, oneTimeKeys
}

// WithRawBody sets the HTTP request body to `body`
func WithRawBody(body []byte) RequestOpt {
	return func(req *http.Request) {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.GetBody = func() (io.ReadCloser, error) {
			r := bytes.NewReader(body)
			return io.NopCloser(r), nil
		}
		// we need to manually set this because we don't set the body
		// in http.NewRequest due to using functional options, and only in NewRequest
		// does the stdlib set this for us.
		req.ContentLength = int64(len(body))
	}
}

// WithContentType sets the HTTP request Content-Type header to `cType`
func WithContentType(cType string) RequestOpt {
	return func(req *http.Request) {
		req.Header.Set("Content-Type", cType)
	}
}

// WithJSONBody sets the HTTP request body to the JSON serialised form of `obj`
func WithJSONBody(t TestLike, obj interface{}) RequestOpt {
	return func(req *http.Request) {
		t.Helper()
		b, err := json.Marshal(obj)
		if err != nil {
			fatalf(t, "CSAPI.Do failed to marshal JSON body: %s", err)
		}
		WithRawBody(b)(req)
	}
}

// WithQueries sets the query parameters on the request.
// This function should not be used to set an "access_token" parameter for Matrix authentication.
// Instead, set CSAPI.AccessToken.
func WithQueries(q url.Values) RequestOpt {
	return func(req *http.Request) {
		req.URL.RawQuery = q.Encode()
	}
}

// WithRetryUntil will retry the request until the provided function returns true. Times out after
// `timeout`, which will then fail the test.
func WithRetryUntil(timeout time.Duration, untilFn func(res *http.Response) bool) RequestOpt {
	return func(req *http.Request) {
		until := req.Context().Value(CtxKeyWithRetryUntil).(*retryUntilParams)
		until.timeout = timeout
		until.untilFn = untilFn
	}
}

// MustDo is the same as Do but fails the test if the returned HTTP response code is not 2xx.
func (c *CSAPI) MustDo(t TestLike, method string, paths []string, opts ...RequestOpt) *http.Response {
	t.Helper()
	res := c.Do(t, method, paths, opts...)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		fatalf(t, "CSAPI.MustDo %s %s returned non-2xx code: %s - body: %s", method, res.Request.URL.String(), res.Status, string(body))
	}
	return res
}

// Do performs an arbitrary HTTP request to the server. This function supports RequestOpts to set
// extra information on the request such as an HTTP request body, query parameters and content-type.
// See all functions in this package starting with `With...`.
//
// Fails the test if an HTTP request could not be made or if there was a network error talking to the
// server. To do assertions on the HTTP response, see the `must` package. For example:
//
//	must.MatchResponse(t, res, match.HTTPResponse{
//		StatusCode: 400,
//		JSON: []match.JSON{
//			match.JSONKeyEqual("errcode", "M_INVALID_USERNAME"),
//		},
//	})
func (c *CSAPI) Do(t TestLike, method string, paths []string, opts ...RequestOpt) *http.Response {
	t.Helper()
	for i := range paths {
		paths[i] = url.PathEscape(paths[i])
	}
	reqURL := c.BaseURL + "/" + strings.Join(paths, "/")
	req, err := http.NewRequest(method, reqURL, nil)
	if err != nil {
		fatalf(t, "CSAPI.Do failed to create http.NewRequest: %s", err)
	}
	// set defaults before RequestOpts
	if c.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	}
	retryUntil := &retryUntilParams{}
	ctx := context.WithValue(req.Context(), CtxKeyWithRetryUntil, retryUntil)
	req = req.WithContext(ctx)

	// set functional options
	for _, o := range opts {
		o(req)
	}
	// set defaults after RequestOpts
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	// debug log the request
	if c.Debug {
		t.Logf("Making %s request to %s (%s)", method, req.URL, c.AccessToken)
		contentType := req.Header.Get("Content-Type")
		if contentType == "application/json" || strings.HasPrefix(contentType, "text/") {
			if req.Body != nil {
				body, _ := io.ReadAll(req.Body)
				t.Logf("Request body: %s", string(body))
				req.Body = io.NopCloser(bytes.NewBuffer(body))
			}
		} else {
			t.Logf("Request body: <binary:%s>", contentType)
		}
	}
	now := time.Now()
	for {
		// Perform the HTTP request
		res, err := c.Client.Do(req)
		if err != nil {
			fatalf(t, "CSAPI.Do response returned error: %s", err)
		}
		// debug log the response
		if c.Debug && res != nil {
			var dump []byte
			dump, err = httputil.DumpResponse(res, true)
			if err != nil {
				fatalf(t, "CSAPI.Do failed to dump response body: %s", err)
			}
			t.Logf("%s", string(dump))
		}
		if retryUntil == nil || retryUntil.timeout == 0 {
			return res // don't retry
		}

		// check the condition, make a copy of the response body first in case the check consumes it
		var resBody []byte
		if res.Body != nil {
			resBody, err = io.ReadAll(res.Body)
			if err != nil {
				fatalf(t, "CSAPI.Do failed to read response body for RetryUntil check: %s", err)
			}
			res.Body = io.NopCloser(bytes.NewBuffer(resBody))
		}
		if retryUntil.untilFn(res) {
			// remake the response and return
			res.Body = io.NopCloser(bytes.NewBuffer(resBody))
			return res
		}
		// condition not satisfied, do we timeout yet?
		if time.Since(now) > retryUntil.timeout {
			fatalf(t, "CSAPI.Do RetryUntil: %v %v timed out after %v", method, req.URL, retryUntil.timeout)
		}
		t.Logf("CSAPI.Do RetryUntil: %v %v response condition not yet met, retrying", method, req.URL)
		// small sleep to avoid tight-looping
		time.Sleep(100 * time.Millisecond)
	}
}

// NewLoggedClient returns an http.Client which logs requests/responses
func NewLoggedClient(t TestLike, hsName string, cli *http.Client) *http.Client {
	t.Helper()
	if cli == nil {
		cli = &http.Client{
			Timeout: 30 * time.Second,
		}
	}
	transport := cli.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	cli.Transport = &loggedRoundTripper{t, hsName, transport}
	return cli
}

type loggedRoundTripper struct {
	t      TestLike
	hsName string
	wrap   http.RoundTripper
}

func (t *loggedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	res, err := t.wrap.RoundTrip(req)
	if err != nil {
		t.t.Logf("[CSAPI] %s %s%s => error: %s (%s)", req.Method, t.hsName, req.URL.Path, err, time.Since(start))
	} else {
		t.t.Logf("[CSAPI] %s %s%s => %s (%s)", req.Method, t.hsName, req.URL.Path, res.Status, time.Since(start))
	}
	return res, err
}

// GetJSONFieldStr extracts a value from a byte-encoded JSON body given a search key
func GetJSONFieldStr(t TestLike, body []byte, wantKey string) string {
	t.Helper()
	res := gjson.GetBytes(body, wantKey)
	if !res.Exists() {
		fatalf(t, "JSONFieldStr: key '%s' missing from %s", wantKey, string(body))
	}
	if res.Str == "" {
		fatalf(t, "JSONFieldStr: key '%s' is not a string, body: %s", wantKey, string(body))
	}
	return res.Str
}

func GetJSONFieldStringArray(t TestLike, body []byte, wantKey string) []string {
	t.Helper()

	res := gjson.GetBytes(body, wantKey)

	if !res.Exists() {
		fatalf(t, "JSONFieldStr: key '%s' missing from %s", wantKey, string(body))
	}

	arrLength := len(res.Array())
	arr := make([]string, arrLength)
	i := 0
	res.ForEach(func(key, value gjson.Result) bool {
		arr[i] = value.Str

		// Keep iterating
		i++
		return true
	})

	return arr
}

// ParseJSON parses a JSON-encoded HTTP Response body into a byte slice
func ParseJSON(t TestLike, res *http.Response) []byte {
	t.Helper()
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		fatalf(t, "MustParseJSON: reading HTTP response body returned %s", err)
	}
	if !gjson.ValidBytes(body) {
		fatalf(t, "MustParseJSON: Response is not valid JSON")
	}
	return body
}

// GjsonEscape escapes . and * from the input so it can be used with gjson.Get
func GjsonEscape(in string) string {
	in = strings.ReplaceAll(in, ".", `\.`)
	in = strings.ReplaceAll(in, "*", `\*`)
	return in
}

func checkArrayElements(object gjson.Result, key string, check func(gjson.Result) bool) error {
	array := object.Get(key)
	if !array.Exists() {
		return fmt.Errorf("Key %s does not exist", key)
	}
	if !array.IsArray() {
		return fmt.Errorf("Key %s exists but it isn't an array", key)
	}
	goArray := array.Array()
	for _, ev := range goArray {
		if check(ev) {
			return nil
		}
	}
	return fmt.Errorf("check function did not pass while iterating over %d elements: %v", len(goArray), array.Raw)
}

// Splits an MXC URI into its origin and media ID parts
func SplitMxc(mxcUri string) (string, string) {
	mxcParts := strings.Split(strings.TrimPrefix(mxcUri, "mxc://"), "/")
	origin := mxcParts[0]
	mediaId := strings.Join(mxcParts[1:], "/")

	return origin, mediaId
}

// SendToDeviceMessages sends to-device messages over /sendToDevice/.
//
// The messages parameter is nested as follows:
// user_id -> device_id -> content (map[string]interface{})
func (c *CSAPI) MustSendToDeviceMessages(t TestLike, evType string, messages map[string]map[string]map[string]interface{}) {
	t.Helper()
	res := c.SendToDeviceMessages(t, evType, messages)
	mustRespond2xx(t, res)
}

// SendToDeviceMessages sends to-device messages over /sendToDevice/.
//
// The messages parameter is nested as follows:
// user_id -> device_id -> content (map[string]interface{})
func (c *CSAPI) SendToDeviceMessages(t TestLike, evType string, messages map[string]map[string]map[string]interface{}) (errRes *http.Response) {
	t.Helper()
	txnID := int(atomic.AddInt64(&c.txnID, 1))
	return c.Do(
		t,
		"PUT",
		[]string{"_matrix", "client", "v3", "sendToDevice", evType, strconv.Itoa(txnID)},
		WithJSONBody(
			t,
			map[string]map[string]map[string]map[string]interface{}{
				"messages": messages,
			},
		),
	)
}

func mustRespond2xx(t TestLike, res *http.Response) {
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return // 2xx
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	fatalf(t, "CSAPI.Must: %s %s returned non-2xx code: %s - body: %s", res.Request.Method, res.Request.URL.String(), res.Status, string(body))
}
