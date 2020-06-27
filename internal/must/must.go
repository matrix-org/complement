// Package must contains assertions for tests
package must

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"testing"

	"github.com/matrix-org/complement/internal/match"
	"github.com/tidwall/gjson"
)

// NotError will ensure `err` is nil else terminate the test with `msg`.
func NotError(t *testing.T, msg string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("MustNotError: %s -> %s", msg, err)
	}
}

// ParseJSON will ensure that the HTTP request/response body is valid JSON, then return the body, else terminate the test.
func ParseJSON(t *testing.T, b io.ReadCloser) []byte {
	t.Helper()
	body, err := ioutil.ReadAll(b)
	if err != nil {
		t.Fatalf("MustParseJSON: reading body returned %s", err)
	}
	if !gjson.ValidBytes(body) {
		t.Fatalf("MustParseJSON: not valid JSON")
	}
	return body
}

// MatchRequest consumes the HTTP request and performs HTTP-level assertions on it. Returns the raw response body.
func MatchRequest(t *testing.T, req *http.Request, m match.HTTPRequest) []byte {
	t.Helper()
	body, err := ioutil.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("MatchRequest: Failed to read request body: %s", err)
	}

	contextStr := fmt.Sprintf("%s => %s", req.URL.String(), string(body))

	if m.Headers != nil {
		for name, val := range m.Headers {
			if req.Header.Get(name) != val {
				t.Fatalf("got %s: %s want %s - %s", name, req.Header.Get(name), val, contextStr)
			}
		}
	}
	if m.JSON != nil {
		if !gjson.ValidBytes(body) {
			t.Fatalf("request body is not valid JSON - %s", contextStr)
		}
		for _, jm := range m.JSON {
			if err = jm(body); err != nil {
				t.Fatalf("%s - %s", err, contextStr)
			}
		}
	}
	return body
}

// MatchResponse consumes the HTTP response and performs HTTP-level assertions on it. Returns the raw response body.
func MatchResponse(t *testing.T, res *http.Response, m match.HTTPResponse) []byte {
	t.Helper()
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("MatchResponse: Failed to read response body: %s", err)
	}

	contextStr := fmt.Sprintf("%s => %s", res.Request.URL.String(), string(body))

	if m.StatusCode != 0 {
		if res.StatusCode != m.StatusCode {
			t.Fatalf("got status %d want %d - %s", res.StatusCode, m.StatusCode, contextStr)
		}
	}
	if m.Headers != nil {
		for name, val := range m.Headers {
			if res.Header.Get(name) != val {
				t.Fatalf("got %s: %s want %s - %s", name, res.Header.Get(name), val, contextStr)
			}
		}
	}
	if m.JSON != nil {
		if !gjson.ValidBytes(body) {
			t.Fatalf("response body is not valid JSON - %s", contextStr)
		}
		for _, jm := range m.JSON {
			if err = jm(body); err != nil {
				t.Fatalf("%s - %s", err, contextStr)
			}
		}
	}
	return body
}

// EqualStr ensures that got==want else logs an error.
func EqualStr(t *testing.T, got, want, msg string) {
	t.Helper()
	if got != want {
		t.Errorf("EqualStr %s: got '%s' want '%s'", msg, got, want)
	}
}

// NotEqualStr ensures that got!=want else logs an error.
func NotEqualStr(t *testing.T, got, want, msg string) {
	t.Helper()
	if got == want {
		t.Errorf("NotEqualStr %s: got '%s', but didn't want it", msg, got)
	}
}

// GetJSONFieldStr extracts the string value under `wantKey` or fails the test.
// The format of `wantKey` is specified at https://godoc.org/github.com/tidwall/gjson#Get
func GetJSONFieldStr(t *testing.T, body []byte, wantKey string) string {
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
