// Package must contains assertions for tests
package must

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/gomatrixserverlib"

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

// MatchSuccess consumes the HTTP response and fails if the response is non-2xx.
func MatchSuccess(t *testing.T, res *http.Response) {
	if res.StatusCode < 200 || res.StatusCode > 299 {
		t.Fatalf("MatchSuccess got status %d instead of a success code", res.StatusCode)
	}
}

// MatchFailure consumes the HTTP response and fails if the response is 2xx.
func MatchFailure(t *testing.T, res *http.Response) {
	if res.StatusCode >= 200 && res.StatusCode <= 299 {
		t.Fatalf("MatchFailure got status %d instead of a failure code", res.StatusCode)
	}
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

// MatchFederationRequest performs JSON assertions on incoming federation requests.
func MatchFederationRequest(t *testing.T, fedReq *gomatrixserverlib.FederationRequest, matchers ...match.JSON) {
	t.Helper()
	content := fedReq.Content()
	if !gjson.ValidBytes(content) {
		t.Fatalf("MatchFederationRequest content is not valid JSON - %s", fedReq.RequestURI())
	}

	for _, jm := range matchers {
		if err := jm(content); err != nil {
			t.Fatalf("MatchFederationRequest %s - %s", err, fedReq.RequestURI())
		}
	}
}

// MatchGJSON performs JSON assertions on a gjson.Result object.
func MatchGJSON(t *testing.T, jsonResult gjson.Result, matchers ...match.JSON) {
	t.Helper()

	MatchJSON(t, jsonResult.Raw, matchers...)
}

// MatchJSON performs JSON assertions on a raw JSON string.
func MatchJSON(t *testing.T, json string, matchers ...match.JSON) {
	t.Helper()

	MatchJSONBytes(t, []byte(json), matchers...)
}

// MatchJSONBytes performs JSON assertions on a raw json byte slice.
func MatchJSONBytes(t *testing.T, rawJson []byte, matchers ...match.JSON) {
	t.Helper()

	if !gjson.ValidBytes(rawJson) {
		t.Fatalf("MatchJSONBytes: rawJson is not valid JSON")
	}

	for _, jm := range matchers {
		if err := jm(rawJson); err != nil {
			t.Fatalf("MatchJSONBytes %s", err)
		}
	}
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

// CheckOffAll checks that a list contains exactly the given items, in any order.
//
// if an item is not present, the test is failed.
// if an item not present in the want list is present, the test is failed.
// Items are compared using match.JSONDeepEqual
func CheckOffAll(t *testing.T, items []interface{}, wantItems []interface{}) {
	t.Helper()
	remaining := CheckOffAllAllowUnwanted(t, items, wantItems)
	if len(remaining) > 0 {
		t.Errorf("CheckOffAll: unexpected items %v", remaining)
	}
}

// CheckOffAllAllowUnwanted checks that a list contains all of the given items, in any order.
// The updated list with the matched items removed from it is returned.
//
// if an item is not present, the test is failed.
// Items are compared using match.JSONDeepEqual
func CheckOffAllAllowUnwanted(t *testing.T, items []interface{}, wantItems []interface{}) []interface{} {
	t.Helper()
	for _, wantItem := range wantItems {
		items = CheckOff(t, items, wantItem)
	}
	return items
}

// CheckOff an item from the list. If the item is not present the test is failed.
// The updated list with the matched item removed from it is returned. Items are
// compared using match.JSONDeepEqual
func CheckOff(t *testing.T, items []interface{}, wantItem interface{}) []interface{} {
	t.Helper()
	// check off the item
	want := -1
	for i, w := range items {
		wBytes, _ := json.Marshal(w)
		if match.JSONDeepEqual(wBytes, wantItem) {
			want = i
			break
		}
	}
	if want == -1 {
		t.Errorf("CheckOff: item %s not present", wantItem)
		return items
	}
	// delete the wanted item
	items = append(items[:want], items[want+1:]...)
	return items
}
