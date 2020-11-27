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

	"github.com/matrix-org/complement/internal/b"
	"github.com/tidwall/gjson"
)

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
	res := c.MustDoRaw(t, "POST", []string{"_matrix", "media", "r0", "upload"}, fileBody, contentType, query)
	body := parseJSON(t, res)
	return getJSONFieldStr(t, body, "content_uri")
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
	body := parseJSON(t, res)
	return getJSONFieldStr(t, body, "room_id")
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
	res := c.MustDoRaw(t, "POST", []string{"_matrix", "client", "r0", "join", roomIDOrAlias}, nil, "application/json", query)
	// return the room ID if we joined with it
	if roomIDOrAlias[0] == '!' {
		return roomIDOrAlias
	}
	// otherwise we should be told the room ID if we joined via an alias
	body := parseJSON(t, res)
	return getJSONFieldStr(t, body, "room_id")
}

// SendEventSynced sends `e` into the room and waits for its event ID to come down /sync.
func (c *CSAPI) SendEventSynced(t *testing.T, roomID string, e b.Event) {
	t.Helper()
	c.txnID++
	paths := []string{"_matrix", "client", "r0", "rooms", roomID, "send", e.Type, strconv.Itoa(c.txnID)}
	if e.StateKey != nil {
		paths = []string{"_matrix", "client", "r0", "rooms", roomID, "state", e.Type, *e.StateKey}
	}
	res := c.MustDo(t, "PUT", paths, e.Content)
	body := parseJSON(t, res)
	eventID := getJSONFieldStr(t, body, "event_id")
	t.Logf("SendEventSynced waiting for event ID %s", eventID)
	c.SyncUntilTimelineHas(t, roomID, func(r gjson.Result) bool {
		return r.Get("event_id").Str == eventID
	})
}

// SyncUntilTimelineHas blocks and continually calls /sync until the `check` function returns true.
// If the `check` function fails the test, the failing event will be automatically logged.
// Will time out after CSAPI.SyncUntilTimeout.
func (c *CSAPI) SyncUntilTimelineHas(t *testing.T, roomID string, check func(gjson.Result) bool) {
	t.Helper()
	c.syncUntil(t, "", "rooms.join."+gjsonEscape(roomID)+".timeline.events", check)
}

func (c *CSAPI) syncUntil(t *testing.T, since, key string, check func(gjson.Result) bool) {
	t.Helper()
	start := time.Now()
	checkCounter := 0
	for {
		if time.Now().Sub(start) > c.SyncUntilTimeout {
			t.Fatalf("syncUntil timed out. Called check function %d times", checkCounter)
		}
		query := url.Values{
			"access_token": []string{c.AccessToken},
			"timeout":      []string{"1000"},
		}
		if since != "" {
			query["since"] = []string{since}
		}
		res, err := c.Do(t, "GET", []string{"_matrix", "client", "r0", "sync"}, nil, query)
		if err != nil {
			t.Fatalf("CSAPI.syncUntil since=%s error: %s", since, err)
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			t.Fatalf("CSAPI.syncUntil since=%s returned HTTP %d", since, res.StatusCode)
		}
		body := parseJSON(t, res)
		since = getJSONFieldStr(t, body, "next_batch")
		keyRes := gjson.GetBytes(body, key)
		if keyRes.IsArray() {
			events := keyRes.Array()
			for _, ev := range events {
				wasFailed := t.Failed()
				if check(ev) {
					if !wasFailed && t.Failed() {
						t.Logf("failing event %s", ev.Raw)
					}
					return
				}
				if !wasFailed && t.Failed() {
					t.Logf("failing event %s", ev.Raw)
				}
				checkCounter++
			}
		}
	}
}

// MustDoWithStatus is the same as MustDo but fails the test if the response code does not match that provided
func (c *CSAPI) MustDoWithStatus(t *testing.T, method string, paths []string, jsonBody interface{}, status int) *http.Response {
	t.Helper()
	res, err := c.DoWithAuth(t, method, paths, jsonBody)
	if err != nil {
		t.Fatalf("CSAPI.MustDoWithStatus %s %s error: %s", method, strings.Join(paths, "/"), err)
	}
	if res.StatusCode != status {
		t.Fatalf("CSAPI.MustDoWithStatus %s %s returned HTTP %d, expected %d", method, res.Request.URL.String(), res.StatusCode, status)
	}
	return res
}

// MustDoWithStatusRaw is the same as MustDoRaw but fails the test if the response code does not match that provided
func (c *CSAPI) MustDoWithStatusRaw(t *testing.T, method string, paths []string, body []byte, contentType string, query url.Values, status int) *http.Response {
	t.Helper()
	res, err := c.DoWithAuthRaw(t, method, paths, body, contentType, query)
	if err != nil {
		t.Fatalf("CSAPI.MustDoWithStatusRaw %s %s error: %s", method, strings.Join(paths, "/"), err)
	}
	if res.StatusCode != status {
		t.Fatalf("CSAPI.MustDoWithStatusRaw %s %s returned HTTP %d, expected %d", method, res.Request.URL.String(), res.StatusCode, status)
	}
	return res
}

// MustDo is the same as Do but fails the test if the response is not 2xx
func (c *CSAPI) MustDo(t *testing.T, method string, paths []string, jsonBody interface{}) *http.Response {
	t.Helper()
	res, err := c.DoWithAuth(t, method, paths, jsonBody)
	if err != nil {
		t.Fatalf("CSAPI.MustDo %s %s error: %s", method, strings.Join(paths, "/"), err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		t.Fatalf("CSAPI.MustDo %s %s returned HTTP %d", method, res.Request.URL.String(), res.StatusCode)
	}
	return res
}

func (c *CSAPI) MustDoRaw(t *testing.T, method string, paths []string, body []byte, contentType string, query url.Values) *http.Response {
	t.Helper()
	res, err := c.DoWithAuthRaw(t, method, paths, body, contentType, query)
	if err != nil {
		t.Fatalf("CSAPI.MustDoRaw %s %s error: %s", method, strings.Join(paths, "/"), err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		t.Fatalf("CSAPI.MustDoRaw %s %s returned HTTP %d", method, res.Request.URL.String(), res.StatusCode)
	}
	return res
}

// DoWithAuth a JSON request. The access token for this user will automatically be added.
func (c *CSAPI) DoWithAuth(t *testing.T, method string, paths []string, jsonBody interface{}) (*http.Response, error) {
	t.Helper()
	var query url.Values
	if c.AccessToken != "" {
		query = url.Values{
			"access_token": []string{c.AccessToken},
		}
	}
	return c.Do(t, method, paths, jsonBody, query)
}

func (c *CSAPI) DoWithAuthRaw(t *testing.T, method string, paths []string, body []byte, contentType string, query url.Values) (*http.Response, error) {
	t.Helper()
	if query == nil {
		query = url.Values{}
	}
	query.Set("access_token", c.AccessToken)
	return c.DoRaw(t, method, paths, body, contentType, query)
}

// Do a JSON request.
func (c *CSAPI) Do(t *testing.T, method string, paths []string, jsonBody interface{}, query url.Values) (*http.Response, error) {
	b, err := json.Marshal(jsonBody)
	if err != nil {
		t.Fatalf("CSAPI.Do failed to marshal JSON body: %s", err)
	}
	return c.DoRaw(t, method, paths, b, "application/json", query)
}

func (c *CSAPI) DoRaw(t *testing.T, method string, paths []string, body []byte, contentType string, query url.Values) (*http.Response, error) {
	t.Helper()
	qs := ""
	if len(query) > 0 {
		qs = "?" + query.Encode()
	}
	for i := range paths {
		paths[i] = url.PathEscape(paths[i])
	}

	reqURL := c.BaseURL + "/" + strings.Join(paths, "/") + qs
	if c.Debug {
		t.Logf("Making %s request to %s", method, reqURL)
		if contentType == "application/json" || strings.HasPrefix(contentType, "text/") {
			t.Logf("Request body: %s", string(body))
		} else {
			t.Logf("Request body: <binary:%s>", contentType)
		}
	}
	req, err := http.NewRequest(method, reqURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("CSAPI.Do failed to create http.NewRequest: %s", err)
	}
	req.Header.Set("Content-Type", contentType)
	res, err := c.Client.Do(req)
	if c.Debug && res != nil {
		dump, err := httputil.DumpResponse(res, true)
		if err != nil {
			t.Fatalf("CSAPI.Do failed to dump response body: %s", err)
		}
		t.Logf("%s", string(dump))

	}
	return res, err
}

// NewLoggedClient returns an http.Client which logs requests/responses
func NewLoggedClient(t *testing.T, cli *http.Client) *http.Client {
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
	cli.Transport = &loggedRoundTripper{t, transport}
	return cli
}

type loggedRoundTripper struct {
	t    *testing.T
	wrap http.RoundTripper
}

func (t *loggedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	res, err := t.wrap.RoundTrip(req)
	if err != nil {
		t.t.Logf("%s %s => error: %s (%s)", req.Method, req.URL.Path, err, time.Now().Sub(start))
	} else {
		t.t.Logf("%s %s => %s (%s)", req.Method, req.URL.Path, res.Status, time.Now().Sub(start))
	}
	return res, err
}

func getJSONFieldStr(t *testing.T, body []byte, wantKey string) string {
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

func parseJSON(t *testing.T, res *http.Response) []byte {
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

// gjsonEscape escapes . and * from the input so it can be used with gjson.Get
func gjsonEscape(in string) string {
	in = strings.ReplaceAll(in, ".", `\.`)
	in = strings.ReplaceAll(in, "*", `\*`)
	return in
}
