package client

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

type CSAPI struct {
	UserID      string
	AccessToken string
	BaseURL     string
	Client      *http.Client
	// how long are we willing to wait for SyncUntil.... calls
	SyncUntilTimeout time.Duration
	// the since token for /sync calls
	Since string
	// True to enable verbose logging
	Debug bool
}

// SyncUntilTimelineHas blocks and continually calls /sync until the `check` function returns true.
// The since token is controlled by CSAPI.Since. If the `check` function fails the test, the failing event
// will be automatically logged. Will time out after CSAPI.SyncUntilTimeout.
func (c *CSAPI) SyncUntilTimelineHas(t *testing.T, roomID string, check func(gjson.Result) bool) {
	t.Helper()
	c.syncUntil(t, "rooms.join."+roomID+".timeline.events", check)
}

func (c *CSAPI) syncUntil(t *testing.T, key string, check func(gjson.Result) bool) {
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
		if c.Since != "" {
			query["since"] = []string{c.Since}
		}
		res, err := c.Do(t, "GET", []string{"_matrix", "client", "r0", "sync"}, nil, query)
		if err != nil {
			t.Fatalf("CSAPI.syncUntil since=%s error: %s", c.Since, err)
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			t.Fatalf("CSAPI.syncUntil since=%s returned HTTP %d", c.Since, res.StatusCode)
		}
		body := parseJSON(t, res)
		c.Since = getJSONFieldStr(t, body, "next_batch")
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

// DoWithAuth a JSON request. The access token for this user will automatically be added.
func (c *CSAPI) DoWithAuth(t *testing.T, method string, paths []string, jsonBody interface{}) (*http.Response, error) {
	t.Helper()
	return c.Do(t, method, paths, jsonBody, url.Values{
		"access_token": []string{c.AccessToken},
	})
}

// Do a JSON request.
func (c *CSAPI) Do(t *testing.T, method string, paths []string, jsonBody interface{}, query url.Values) (*http.Response, error) {
	t.Helper()
	qs := ""
	if len(query) > 0 {
		qs = "?" + query.Encode()
	}
	for i := range paths {
		paths[i] = url.PathEscape(paths[i])
	}

	reqURL := c.BaseURL + "/" + strings.Join(paths, "/") + qs
	b, err := json.Marshal(jsonBody)
	if err != nil {
		t.Fatalf("CSAPI.Do failed to marshal JSON body: %s", err)
	}
	if c.Debug {
		t.Logf("Making %s request to %s", method, reqURL)
		t.Logf("Request body: %s", string(b))
	}
	req, err := http.NewRequest(method, reqURL, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("CSAPI.Do failed to create http.NewRequest: %s", err)
	}
	req.Header.Set("Content-Type", "application/json")
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
	res, err := t.wrap.RoundTrip(req)
	if err != nil {
		t.t.Logf("%s %s => error: %s", req.Method, req.URL.String(), err)
	} else {
		t.t.Logf("%s %s => HTTP %s", req.Method, req.URL.String(), res.Status)
	}
	return res, err
}

func getJSONFieldStr(t *testing.T, body []byte, wantKey string) string {
	t.Helper()
	res := gjson.GetBytes(body, wantKey)
	if res.Index == 0 {
		t.Fatalf("JSONFieldStr: key '%s' missing from %s", wantKey, string(body))
	}
	if res.Str == "" {
		t.Fatalf("JSONFieldStr: key '%s' is not a string, body: %s", wantKey, string(body))
	}
	return res.Str
}

func parseJSON(t *testing.T, res *http.Response) []byte {
	t.Helper()
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("MustParseJSON: reading HTTP response body returned %s", err)
	}
	if !gjson.ValidBytes(body) {
		t.Fatalf("MustParseJSON: Response is not valid JSON")
	}
	return body
}
