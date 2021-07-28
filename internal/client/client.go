package client

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
)

// RequestOpt is a functional option which will modify an outgoing HTTP request.
// See functions starting with `With...` in this package for more info.
type RequestOpt func(req *http.Request)

type CSAPI struct {
	UserID      string
	AccessToken string
	BaseURL     string
	Client      *http.Client
	// how long are we willing to wait for SyncUntil.... calls
	SyncUntilTimeout time.Duration
	// True to enable verbose logging
	Debug bool

	txnID int
}

// UploadContent uploads the provided content with an optional file name. Fails the test on error. Returns the MXC URI.
func (c *CSAPI) UploadContent(t *testing.T, fileBody []byte, fileName string, contentType string) string {
	t.Helper()
	query := url.Values{}
	if fileName != "" {
		query.Set("filename", fileName)
	}
	res := c.MustDoFunc(
		t, "POST", []string{"_matrix", "media", "r0", "upload"},
		WithRawBody(fileBody), WithContentType(contentType), WithQueries(query),
	)
	body := ParseJSON(t, res)
	return GetJSONFieldStr(t, body, "content_uri")
}

// DownloadContent downloads media from the server, returning the raw bytes and the Content-Type. Fails the test on error.
func (c *CSAPI) DownloadContent(t *testing.T, mxcUri string) ([]byte, string) {
	t.Helper()
	mxcParts := strings.Split(strings.TrimPrefix(mxcUri, "mxc://"), "/")
	origin := mxcParts[0]
	mediaId := strings.Join(mxcParts[1:], "/")
	res := c.MustDo(t, "GET", []string{"_matrix", "media", "r0", "download", origin, mediaId}, struct{}{})
	contentType := res.Header.Get("Content-Type")
	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}
	return b, contentType
}

// CreateRoom creates a room with an optional HTTP request body. Fails the test on error. Returns the room ID.
func (c *CSAPI) CreateRoom(t *testing.T, creationContent interface{}) string {
	t.Helper()
	res := c.MustDo(t, "POST", []string{"_matrix", "client", "r0", "createRoom"}, creationContent)
	body := ParseJSON(t, res)
	return GetJSONFieldStr(t, body, "room_id")
}

// JoinRoom joins the room ID or alias given, else fails the test. Returns the room ID.
func (c *CSAPI) JoinRoom(t *testing.T, roomIDOrAlias string, serverNames []string) string {
	t.Helper()
	// construct URL query parameters
	query := make(url.Values, len(serverNames))
	for _, serverName := range serverNames {
		query.Add("server_name", serverName)
	}
	// join the room
	res := c.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "join", roomIDOrAlias}, WithQueries(query))
	// return the room ID if we joined with it
	if roomIDOrAlias[0] == '!' {
		return roomIDOrAlias
	}
	// otherwise we should be told the room ID if we joined via an alias
	body := ParseJSON(t, res)
	return GetJSONFieldStr(t, body, "room_id")
}

// LeaveRoom joins the room ID, else fails the test.
func (c *CSAPI) LeaveRoom(t *testing.T, roomID string) {
	t.Helper()
	// leave the room
	c.MustDoFunc(t, "POST", []string{"_matrix", "client", "r0", "rooms", roomID, "leave"})
}

// InviteRoom invites userID to the room ID, else fails the test.
func (c *CSAPI) InviteRoom(t *testing.T, roomID string, userID string) {
	t.Helper()
	// Invite the user to the room
	body := map[string]interface{}{
		"user_id": userID,
	}
	c.MustDo(t, "POST", []string{"_matrix", "client", "r0", "rooms", roomID, "invite"}, body)
}

// SendEventSynced sends `e` into the room and waits for its event ID to come down /sync.
// Returns the event ID of the sent event.
func (c *CSAPI) SendEventSynced(t *testing.T, roomID string, e b.Event) string {
	t.Helper()
	c.txnID++
	paths := []string{"_matrix", "client", "r0", "rooms", roomID, "send", e.Type, strconv.Itoa(c.txnID)}
	if e.StateKey != nil {
		paths = []string{"_matrix", "client", "r0", "rooms", roomID, "state", e.Type, *e.StateKey}
	}
	res := c.MustDo(t, "PUT", paths, e.Content)
	body := ParseJSON(t, res)
	eventID := GetJSONFieldStr(t, body, "event_id")
	t.Logf("SendEventSynced waiting for event ID %s", eventID)
	c.SyncUntilTimelineHas(t, roomID, func(r gjson.Result) bool {
		return r.Get("event_id").Str == eventID
	})
	return eventID
}

// SyncUntilTimelineHas blocks and continually calls /sync until the `check` function returns true.
// If the `check` function fails the test, the failing event will be automatically logged.
// Will time out after CSAPI.SyncUntilTimeout.
func (c *CSAPI) SyncUntilTimelineHas(t *testing.T, roomID string, check func(gjson.Result) bool) {
	t.Helper()
	c.SyncUntil(t, "", "", "rooms.join."+GjsonEscape(roomID)+".timeline.events", check)
}

// SyncUntil blocks and continually calls /sync until the `check` function returns true.
// If the `check` function fails the test, the failing event will be automatically logged.
// Will time out after CSAPI.SyncUntilTimeout.
func (c *CSAPI) SyncUntil(t *testing.T, since, filter, key string, check func(gjson.Result) bool) {
	t.Helper()
	start := time.Now()
	checkCounter := 0
	// Print failing events in a defer() so we handle t.Fatalf in the same way as t.Errorf
	var wasFailed = t.Failed()
	var lastEvent *gjson.Result
	timedOut := false
	defer func() {
		if !wasFailed && t.Failed() {
			raw := ""
			if lastEvent != nil {
				raw = lastEvent.Raw
			}
			if !timedOut {
				t.Logf("SyncUntil: failing event %s", raw)
			}
		}
	}()
	for {
		if time.Since(start) > c.SyncUntilTimeout {
			timedOut = true
			t.Fatalf("SyncUntil: timed out. Called check function %d times", checkCounter)
		}
		query := url.Values{
			"timeout": []string{"1000"},
		}
		if since != "" {
			query["since"] = []string{since}
		}
		if filter != "" {
			query["filter"] = []string{filter}
		}
		res := c.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "sync"}, WithQueries(query))
		body := ParseJSON(t, res)
		since = GetJSONFieldStr(t, body, "next_batch")
		keyRes := gjson.GetBytes(body, key)
		if keyRes.IsArray() {
			events := keyRes.Array()
			for i, ev := range events {
				lastEvent = &events[i]
				if check(ev) {
					return
				}
				wasFailed = t.Failed()
				checkCounter++
			}
		}
	}
}

//RegisterUser will register the user with given parameters and
// return user ID & access token, and fail the test on network error
func (c *CSAPI) RegisterUser(t *testing.T, localpart, password string) (userID, accessToken string) {
	t.Helper()
	reqBody := map[string]interface{}{
		"auth": map[string]string{
			"type": "m.login.dummy",
		},
		"username": localpart,
		"password": password,
	}
	res := c.MustDo(t, "POST", []string{"_matrix", "client", "r0", "register"}, reqBody)

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("unable to read response body: %v", err)
	}

	userID = gjson.GetBytes(body, "user_id").Str
	accessToken = gjson.GetBytes(body, "access_token").Str
	return userID, accessToken
}

// MustDo will do the HTTP request and fail the test if the response is not 2xx
func (c *CSAPI) MustDo(t *testing.T, method string, paths []string, jsonBody interface{}) *http.Response {
	t.Helper()
	res := c.DoFunc(t, method, paths, WithJSONBody(t, jsonBody))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		t.Fatalf("CSAPI.MustDo %s %s returned HTTP %d", method, res.Request.URL.String(), res.StatusCode)
	}
	return res
}

// WithRawBody sets the HTTP request body to `body`
func WithRawBody(body []byte) RequestOpt {
	return func(req *http.Request) {
		req.Body = ioutil.NopCloser(bytes.NewBuffer(body))
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
func WithJSONBody(t *testing.T, obj interface{}) RequestOpt {
	return func(req *http.Request) {
		t.Helper()
		b, err := json.Marshal(obj)
		if err != nil {
			t.Fatalf("CSAPI.Do failed to marshal JSON body: %s", err)
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

// MustDoFunc is the same as DoFunc but fails the test if the returned HTTP response code is not 2xx.
func (c *CSAPI) MustDoFunc(t *testing.T, method string, paths []string, opts ...RequestOpt) *http.Response {
	t.Helper()
	res := c.DoFunc(t, method, paths, opts...)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		t.Fatalf("CSAPI.MustDoFunc response return non-2xx code: %s", res.Status)
	}
	return res
}

// DoFunc performs an arbitrary HTTP request to the server. This function supports RequestOpts to set
// extra information on the request such as an HTTP request body, query parameters and content-type.
// See all functions in this package starting with `With...`.
//
// Fails the test if an HTTP request could not be made or if there was a network error talking to the
// server. To do assertions on the HTTP response, see the `must` package. For example:
//    must.MatchResponse(t, res, match.HTTPResponse{
//    	StatusCode: 400,
//    	JSON: []match.JSON{
//    		match.JSONKeyEqual("errcode", "M_INVALID_USERNAME"),
//    	},
//    })
func (c *CSAPI) DoFunc(t *testing.T, method string, paths []string, opts ...RequestOpt) *http.Response {
	t.Helper()
	for i := range paths {
		paths[i] = url.PathEscape(paths[i])
	}
	reqURL := c.BaseURL + "/" + strings.Join(paths, "/")
	req, err := http.NewRequest(method, reqURL, nil)
	if err != nil {
		t.Fatalf("CSAPI.DoFunc failed to create http.NewRequest: %s", err)
	}
	// set defaults before RequestOpts
	if c.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	}

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
		t.Logf("Making %s request to %s", method, reqURL)
		contentType := req.Header.Get("Content-Type")
		if contentType == "application/json" || strings.HasPrefix(contentType, "text/") {
			if req.Body != nil {
				body, _ := ioutil.ReadAll(req.Body)
				t.Logf("Request body: %s", string(body))
				req.Body = ioutil.NopCloser(bytes.NewBuffer(body))
			}
		} else {
			t.Logf("Request body: <binary:%s>", contentType)
		}
	}
	// Perform the HTTP request
	res, err := c.Client.Do(req)
	if err != nil {
		t.Fatalf("CSAPI.DoFunc response returned error: %s", err)
	}
	// debug log the response
	if c.Debug && res != nil {
		var dump []byte
		dump, err = httputil.DumpResponse(res, true)
		if err != nil {
			t.Fatalf("CSAPI.DoFunc failed to dump response body: %s", err)
		}
		t.Logf("%s", string(dump))
	}
	return res
}

// NewLoggedClient returns an http.Client which logs requests/responses
func NewLoggedClient(t *testing.T, hsName string, cli *http.Client) *http.Client {
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
	t      *testing.T
	hsName string
	wrap   http.RoundTripper
}

func (t *loggedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	res, err := t.wrap.RoundTrip(req)
	if err != nil {
		t.t.Logf("%s %s%s => error: %s (%s)", req.Method, t.hsName, req.URL.Path, err, time.Since(start))
	} else {
		t.t.Logf("%s %s%s => %s (%s)", req.Method, t.hsName, req.URL.Path, res.Status, time.Since(start))
	}
	return res, err
}

// GetJSONFieldStr extracts a value from a byte-encoded JSON body given a search key
func GetJSONFieldStr(t *testing.T, body []byte, wantKey string) string {
	t.Helper()
	res := gjson.GetBytes(body, wantKey)
	if !res.Exists() {
		t.Fatalf("JSONFieldStr: key '%s' missing from %s", wantKey, string(body))
	}
	if res.Str == "" {
		t.Fatalf("JSONFieldStr: key '%s' is not a string, body: %s", wantKey, string(body))
	}
	return res.Str
}

func GetJSONFieldStringArray(t *testing.T, body []byte, wantKey string) []string {
	t.Helper()

	res := gjson.GetBytes(body, wantKey)

	if !res.Exists() {
		t.Fatalf("JSONFieldStr: key '%s' missing from %s", wantKey, string(body))
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
func ParseJSON(t *testing.T, res *http.Response) []byte {
	t.Helper()
	defer res.Body.Close()
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("MustParseJSON: reading HTTP response body returned %s", err)
	}
	if !gjson.ValidBytes(body) {
		t.Fatalf("MustParseJSON: Response is not valid JSON")
	}
	return body
}

// GjsonEscape escapes . and * from the input so it can be used with gjson.Get
func GjsonEscape(in string) string {
	in = strings.ReplaceAll(in, ".", `\.`)
	in = strings.ReplaceAll(in, "*", `\*`)
	return in
}
