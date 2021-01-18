// Package must contains assertions for tests
package must

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/match"
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
				t.Fatalf("MatchRequest got %s: %s want %s - %s", name, req.Header.Get(name), val, contextStr)
			}
		}
	}
	if m.JSON != nil {
		if !gjson.ValidBytes(body) {
			t.Fatalf("MatchRequest request body is not valid JSON - %s", contextStr)
		}
		for _, jm := range m.JSON {
			if err = jm(body); err != nil {
				t.Fatalf("MatchRequest %s - %s", err, contextStr)
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
			t.Fatalf("MatchResponse got status %d want %d - %s", res.StatusCode, m.StatusCode, contextStr)
		}
	}
	if m.Headers != nil {
		for name, val := range m.Headers {
			if res.Header.Get(name) != val {
				t.Fatalf("MatchResponse got %s: %s want %s - %s", name, res.Header.Get(name), val, contextStr)
			}
		}
	}
	if m.JSON != nil {
		if !gjson.ValidBytes(body) {
			t.Fatalf("MatchResponse response body is not valid JSON - %s", contextStr)
		}
		for _, jm := range m.JSON {
			if err = jm(body); err != nil {
				t.Fatalf("MatchResponse %s - %s", err, contextStr)
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

// StartWithStr ensures that got starts with wantPrefix else logs an error.
func StartWithStr(t *testing.T, got, wantPrefix, msg string) {
	t.Helper()
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("StartWithStr: %s: got '%s' without prefix '%s'", msg, got, wantPrefix)
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

// HaveInOrder checks that the two string slices match exactly, failing the test on mismatches or omissions.
func HaveInOrder(t *testing.T, gots []string, wants []string) {
	t.Helper()
	if len(gots) != len(wants) {
		t.Fatalf("HaveEventsInOrder: length mismatch, got %v want %v", gots, wants)
	}
	for i := range gots {
		if gots[i] != wants[i] {
			t.Errorf("HaveEventsInOrder: index %d got %s want %s", i, gots[i], wants[i])
		}
	}
}

// CheckOff an item from the list. If the item is not present the test is failed.
// The updated list with the matched item removed from it is returned. Items are
// compared using reflect.DeepEqual
func CheckOff(t *testing.T, items []interface{}, wantItem interface{}) []interface{} {
	// check off the item
	want := -1
	for i, w := range items {
		if reflect.DeepEqual(w, wantItem) {
			want = i
			break
		}
	}
	if want == -1 {
		t.Errorf("CheckOff: unexpected item %s", wantItem)
		return items
	}
	// delete the wanted item
	items = append(items[:want], items[want+1:]...)
	return items
}
