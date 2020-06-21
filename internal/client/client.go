package client

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type CSAPI struct {
	UserID      string
	AccessToken string
	BaseURL     string
	Client      *http.Client
}

// MustDo is the same as Do but fails the test if the response is not 2xx
func (c *CSAPI) MustDo(t *testing.T, method string, paths []string, jsonBody interface{}) *http.Response {
	t.Helper()
	res, err := c.Do(t, method, paths, jsonBody)
	if err != nil {
		t.Fatalf("CSAPI.MustDo %s %s error: %s", method, strings.Join(paths, "/"), err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		t.Fatalf("CSAPI.MustDo %s %s returned HTTP %d", method, res.Request.URL.String(), res.StatusCode)
	}
	return res
}

// Do a JSON request. The access token for this user will automatically be added.
func (c *CSAPI) Do(t *testing.T, method string, paths []string, jsonBody interface{}) (*http.Response, error) {
	t.Helper()
	qs := ""
	if c.AccessToken != "" {
		query := url.Values{
			"access_token": []string{c.AccessToken},
		}
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
	req, err := http.NewRequest(method, reqURL, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("CSAPI.Do failed to create http.NewRequest: %s", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.Client.Do(req)
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
	t.t.Logf("%s %s", req.Method, req.URL.String())
	res, err := t.wrap.RoundTrip(req)
	if err != nil {
		t.t.Logf("%s %s => error: %s", req.Method, req.URL.String(), err)
	} else {
		t.t.Logf("%s %s => HTTP %s", req.Method, req.URL.String(), res.Status)
	}
	return res, err
}
