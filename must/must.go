// Package must contains assertions for tests
package must

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"golang.org/x/exp/slices"

	"github.com/matrix-org/gomatrixserverlib/fclient"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/match"
)

// NotError will ensure `err` is nil else terminate the test with `msg`.
func NotError(t *testing.T, msg string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("MustNotError: %s -> %s", msg, err)
	}
}

// EXPERIMENTAL
// ParseJSON will ensure that the HTTP request/response body is valid JSON, then return the body, else terminate the test.
func ParseJSON(t *testing.T, b io.ReadCloser) gjson.Result {
	t.Helper()
	body, err := io.ReadAll(b)
	if err != nil {
		t.Fatalf("MustParseJSON: reading body returned %s", err)
	}
	if !gjson.ValidBytes(body) {
		t.Fatalf("MustParseJSON: not valid JSON")
	}
	return gjson.ParseBytes(body)
}

// EXPERIMENTAL
// MatchRequest consumes the HTTP request and performs HTTP-level assertions on it. Returns the raw response body.
func MatchRequest(t *testing.T, req *http.Request, m match.HTTPRequest) []byte {
	t.Helper()
	body, err := io.ReadAll(req.Body)
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
		parsedBody := gjson.ParseBytes(body)
		for _, jm := range m.JSON {
			if err = jm(parsedBody); err != nil {
				t.Fatalf("MatchRequest %s - %s", err, contextStr)
			}
		}
	}
	return body
}

// EXPERIMENTAL
// MatchSuccess consumes the HTTP response and fails if the response is non-2xx.
func MatchSuccess(t *testing.T, res *http.Response) {
	if res.StatusCode < 200 || res.StatusCode > 299 {
		t.Fatalf("MatchSuccess got status %d instead of a success code", res.StatusCode)
	}
}

// EXPERIMENTAL
// MatchFailure consumes the HTTP response and fails if the response is 2xx.
func MatchFailure(t *testing.T, res *http.Response) {
	if res.StatusCode >= 200 && res.StatusCode <= 299 {
		t.Fatalf("MatchFailure got status %d instead of a failure code", res.StatusCode)
	}
}

// EXPERIMENTAL
// MatchResponse consumes the HTTP response and performs HTTP-level assertions on it. Returns the raw response body.
func MatchResponse(t *testing.T, res *http.Response, m match.HTTPResponse) []byte {
	t.Helper()
	body, err := io.ReadAll(res.Body)
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
		parsedBody := gjson.ParseBytes(body)
		for _, jm := range m.JSON {
			if err = jm(parsedBody); err != nil {
				t.Fatalf("MatchResponse %s - %s", err, contextStr)
			}
		}
	}
	return body
}

// MatchFederationRequest performs JSON assertions on incoming federation requests.
func MatchFederationRequest(t *testing.T, fedReq *fclient.FederationRequest, matchers ...match.JSON) {
	t.Helper()
	content := fedReq.Content()
	if !gjson.ValidBytes(content) {
		t.Fatalf("MatchFederationRequest content is not valid JSON - %s", fedReq.RequestURI())
	}

	parsedContent := gjson.ParseBytes(content)
	for _, jm := range matchers {
		if err := jm(parsedContent); err != nil {
			t.Fatalf("MatchFederationRequest %s - %s", err, fedReq.RequestURI())
		}
	}
}

// EXPERIMENTAL
// MatchGJSON performs JSON assertions on a gjson.Result object.
func MatchGJSON(t *testing.T, jsonResult gjson.Result, matchers ...match.JSON) {
	t.Helper()

	MatchJSONBytes(t, []byte(jsonResult.Raw), matchers...)
}

// EXPERIMENTAL
// MatchJSONBytes performs JSON assertions on a raw json byte slice.
func MatchJSONBytes(t *testing.T, rawJson []byte, matchers ...match.JSON) {
	t.Helper()

	if !gjson.ValidBytes(rawJson) {
		t.Fatalf("MatchJSONBytes: rawJson is not valid JSON")
	}

	body := gjson.ParseBytes(rawJson)
	for _, jm := range matchers {
		if err := jm(body); err != nil {
			t.Fatalf("MatchJSONBytes %s with input = %v", err, string(rawJson))
		}
	}
}

// Equal ensures that got==want else logs an error.
// The 'msg' is displayed with the error to provide extra context.
func Equal[V comparable](t *testing.T, got, want V, msg string) {
	t.Helper()
	if got != want {
		t.Errorf("Equal %s: got '%v' want '%v'", msg, got, want)
	}
}

// NotEqual ensures that got!=want else logs an error.
// The 'msg' is displayed with the error to provide extra context.
func NotEqual[V comparable](t *testing.T, got, want V, msg string) {
	t.Helper()
	if got == want {
		t.Errorf("NotEqual %s: got '%v', want '%v'", msg, got, want)
	}
}

// EXPERIMENTAL
// StartWithStr ensures that got starts with wantPrefix else logs an error.
func StartWithStr(t *testing.T, got, wantPrefix, msg string) {
	t.Helper()
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("StartWithStr: %s: got '%s' without prefix '%s'", msg, got, wantPrefix)
	}
}

// GetJSONFieldStr extracts the string value under `wantKey` or fails the test.
// The format of `wantKey` is specified at https://godoc.org/github.com/tidwall/gjson#Get
func GetJSONFieldStr(t *testing.T, body gjson.Result, wantKey string) string {
	t.Helper()
	res := body.Get(wantKey)
	if res.Index == 0 {
		t.Fatalf("JSONFieldStr: key '%s' missing from %s", wantKey, body.Raw)
	}
	if res.Str == "" {
		t.Fatalf("JSONFieldStr: key '%s' is not a string, body: %s", wantKey, body.Raw)
	}
	return res.Str
}

// EXPERIMENTAL
// HaveInOrder checks that the two slices match exactly, failing the test on mismatches or omissions.
func HaveInOrder[V comparable](t *testing.T, gots []V, wants []V) {
	t.Helper()
	if len(gots) != len(wants) {
		t.Fatalf("HaveInOrder: length mismatch, got %v want %v", gots, wants)
	}
	for i := range gots {
		if gots[i] != wants[i] {
			t.Errorf("HaveInOrder: index %d got %v want %v", i, gots[i], wants[i])
		}
	}
}

// EXPERIMENTAL
// ContainSubset checks that every item in smaller is in larger, failing the test if at least 1 item isn't. Ignores extra elements
// in larger. Ignores ordering.
func ContainSubset[V comparable](t *testing.T, larger []V, smaller []V) {
	t.Helper()
	if len(larger) < len(smaller) {
		t.Fatalf("ContainSubset: length mismatch, larger=%d smaller=%d", len(larger), len(smaller))
	}
	for i, item := range smaller {
		if !slices.Contains(larger, item) {
			t.Fatalf("ContainSubset: element not found in larger set: smaller[%d] (%v) larger=%v", i, item, larger)
		}
	}
}

// EXPERIMENTAL
// NotContainSubset checks that every item in smaller is NOT in larger, failing the test if at least 1 item is. Ignores extra elements
// in larger. Ignores ordering.
func NotContainSubset[V comparable](t *testing.T, larger []V, smaller []V) {
	t.Helper()
	if len(larger) < len(smaller) {
		t.Fatalf("NotContainSubset: length mismatch, larger=%d smaller=%d", len(larger), len(smaller))
	}
	for i, item := range smaller {
		if slices.Contains(larger, item) {
			t.Fatalf("NotContainSubset: element found in larger set: smaller[%d] (%v)", i, item)
		}
	}
}

// EXPERIMENTAL
// GetTimelineEventIDs returns the timeline event IDs in the sync response for the given room ID. If the room is missing
// this returns a 0 element slice.
func GetTimelineEventIDs(t *testing.T, topLevelSyncJSON gjson.Result, roomID string) []string {
	timeline := topLevelSyncJSON.Get(fmt.Sprintf("rooms.join.%s.timeline.events", client.GjsonEscape(roomID))).Array()
	eventIDs := make([]string, len(timeline))
	for i := range timeline {
		eventIDs[i] = timeline[i].Get("event_id").Str
	}
	return eventIDs
}

// EXPERIMENTAL
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

// EXPERIMENTAL
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

// EXPERIMENTAL
// CheckOff an item from the list. If the item is not present the test is failed.
// The updated list with the matched item removed from it is returned. Items are
// compared using JSON deep equal.
func CheckOff(t *testing.T, items []interface{}, wantItem interface{}) []interface{} {
	t.Helper()
	// check off the item
	want := -1
	for i, w := range items {
		wBytes, _ := json.Marshal(w)
		if jsonDeepEqual(wBytes, wantItem) {
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

func jsonDeepEqual(gotJson []byte, wantValue interface{}) bool {
	// marshal what the test gave us
	wantBytes, _ := json.Marshal(wantValue)
	// re-marshal what the network gave us to acount for key ordering
	var gotVal interface{}
	_ = json.Unmarshal(gotJson, &gotVal)
	gotBytes, _ := json.Marshal(gotVal)
	return bytes.Equal(gotBytes, wantBytes)
}
