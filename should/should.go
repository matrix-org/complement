// Package should contains assertions for tests, which returns an error if the assertion fails.
package should

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/tidwall/gjson"
	"golang.org/x/exp/slices"

	"github.com/matrix-org/gomatrixserverlib/fclient"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/match"
)

// EXPERIMENTAL
// ParseJSON will ensure that the HTTP request/response body is valid JSON, then return the body, else returns an error.
func ParseJSON(b io.ReadCloser) (res gjson.Result, err error) {
	body, err := io.ReadAll(b)
	if err != nil {
		return res, fmt.Errorf("ParseJSON: reading body returned %s", err)
	}
	if !gjson.ValidBytes(body) {
		return res, fmt.Errorf("ParseJSON: not valid JSON")
	}
	return gjson.ParseBytes(body), nil
}

// EXPERIMENTAL
// MatchRequest consumes the HTTP request and performs HTTP-level assertions on it. Returns the raw response body.
func MatchRequest(req *http.Request, m match.HTTPRequest) ([]byte, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("MatchRequest: Failed to read request body: %s", err)
	}

	contextStr := fmt.Sprintf("%s => %s", req.URL.String(), string(body))

	if m.Headers != nil {
		for name, val := range m.Headers {
			if req.Header.Get(name) != val {
				return nil, fmt.Errorf("MatchRequest got %s: %s want %s - %s", name, req.Header.Get(name), val, contextStr)
			}
		}
	}
	if m.JSON != nil {
		if !gjson.ValidBytes(body) {
			return nil, fmt.Errorf("MatchRequest request body is not valid JSON - %s", contextStr)
		}
		parsedBody := gjson.ParseBytes(body)
		for _, jm := range m.JSON {
			if err = jm(parsedBody); err != nil {
				return nil, fmt.Errorf("MatchRequest %s - %s", err, contextStr)
			}
		}
	}
	return body, nil
}

// EXPERIMENTAL
// MatchSuccess consumes the HTTP response and fails if the response is non-2xx.
func MatchSuccess(res *http.Response) error {
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("MatchSuccess got status %d instead of a success code", res.StatusCode)
	}
	return nil
}

// EXPERIMENTAL
// MatchFailure consumes the HTTP response and fails if the response is 2xx.
func MatchFailure(res *http.Response) error {
	if res.StatusCode >= 200 && res.StatusCode <= 299 {
		return fmt.Errorf("MatchFailure got status %d instead of a failure code", res.StatusCode)
	}
	return nil
}

// EXPERIMENTAL
// MatchResponse consumes the HTTP response and performs HTTP-level assertions on it. Returns the raw response body.
func MatchResponse(res *http.Response, m match.HTTPResponse) ([]byte, error) {
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("MatchResponse: Failed to read response body: %s", err)
	}

	contextStr := fmt.Sprintf("%s => %s", res.Request.URL.String(), string(body))

	if m.StatusCode != 0 {
		if res.StatusCode != m.StatusCode {
			return nil, fmt.Errorf("MatchResponse got status %d want %d - %s", res.StatusCode, m.StatusCode, contextStr)
		}
	}
	if m.Headers != nil {
		for name, val := range m.Headers {
			if res.Header.Get(name) != val {
				return nil, fmt.Errorf("MatchResponse got %s: %s want %s - %s", name, res.Header.Get(name), val, contextStr)
			}
		}
	}
	if m.JSON != nil {
		if !gjson.ValidBytes(body) {
			return nil, fmt.Errorf("MatchResponse response body is not valid JSON - %s", contextStr)
		}
		parsedBody := gjson.ParseBytes(body)
		for _, jm := range m.JSON {
			if err = jm(parsedBody); err != nil {
				return nil, fmt.Errorf("MatchResponse %s - %s", err, contextStr)
			}
		}
	}
	return body, nil
}

// MatchFederationRequest performs JSON assertions on incoming federation requests.
func MatchFederationRequest(fedReq *fclient.FederationRequest, matchers ...match.JSON) error {
	content := fedReq.Content()
	if !gjson.ValidBytes(content) {
		return fmt.Errorf("MatchFederationRequest content is not valid JSON - %s", fedReq.RequestURI())
	}

	parsedContent := gjson.ParseBytes(content)
	for _, jm := range matchers {
		if err := jm(parsedContent); err != nil {
			return fmt.Errorf("MatchFederationRequest %s - %s", err, fedReq.RequestURI())
		}
	}
	return nil
}

// EXPERIMENTAL
// MatchGJSON performs JSON assertions on a gjson.Result object.
func MatchGJSON(jsonResult gjson.Result, matchers ...match.JSON) error {
	return MatchJSONBytes([]byte(jsonResult.Raw), matchers...)
}

// EXPERIMENTAL
// MatchJSONBytes performs JSON assertions on a raw json byte slice.
func MatchJSONBytes(rawJson []byte, matchers ...match.JSON) error {
	if !gjson.ValidBytes(rawJson) {
		return fmt.Errorf("MatchJSONBytes: rawJson is not valid JSON")
	}

	body := gjson.ParseBytes(rawJson)
	for _, jm := range matchers {
		if err := jm(body); err != nil {
			return fmt.Errorf("MatchJSONBytes %s with input = %v", err, body.Get("@pretty").String())
		}
	}
	return nil
}

// EXPERIMENTAL
// GetJSONFieldStr extracts the string value under `wantKey` or fails the test.
// The format of `wantKey` is specified at https://godoc.org/github.com/tidwall/gjson#Get
func GetJSONFieldStr(body gjson.Result, wantKey string) (string, error) {
	res := body.Get(wantKey)
	if res.Index == 0 {
		return "", fmt.Errorf("JSONFieldStr: key '%s' missing from %s", wantKey, body.Raw)
	}
	if res.Str == "" {
		return "", fmt.Errorf("JSONFieldStr: key '%s' is not a string, body: %s", wantKey, body.Raw)
	}
	return res.Str, nil
}

// EXPERIMENTAL
// HaveInOrder checks that the two slices match exactly, failing the test on mismatches or omissions.
func HaveInOrder[V comparable](gots []V, wants []V) error {
	if len(gots) != len(wants) {
		return fmt.Errorf("HaveInOrder: length mismatch, got %v want %v", gots, wants)
	}
	for i := range gots {
		if gots[i] != wants[i] {
			return fmt.Errorf("HaveInOrder: index %d got %v want %v", i, gots[i], wants[i])
		}
	}
	return nil
}

// EXPERIMENTAL
// ContainSubset checks that every item in smaller is in larger, failing the test if at least 1 item isn't. Ignores extra elements
// in larger. Ignores ordering.
func ContainSubset[V comparable](larger []V, smaller []V) error {
	if len(larger) < len(smaller) {
		return fmt.Errorf("ContainSubset: length mismatch, larger=%d smaller=%d", len(larger), len(smaller))
	}
	for i, item := range smaller {
		if !slices.Contains(larger, item) {
			return fmt.Errorf("ContainSubset: element not found in larger set: smaller[%d] (%v) larger=%v", i, item, larger)
		}
	}
	return nil
}

// EXPERIMENTAL
// NotContainSubset checks that every item in smaller is NOT in larger, failing the test if at least 1 item is. Ignores extra elements
// in larger. Ignores ordering.
func NotContainSubset[V comparable](larger []V, smaller []V) error {
	if len(larger) < len(smaller) {
		return fmt.Errorf("NotContainSubset: length mismatch, larger=%d smaller=%d", len(larger), len(smaller))
	}
	for i, item := range smaller {
		if slices.Contains(larger, item) {
			return fmt.Errorf("NotContainSubset: element found in larger set: smaller[%d] (%v)", i, item)
		}
	}
	return nil
}

// EXPERIMENTAL
// GetTimelineEventIDs returns the timeline event IDs in the sync response for the given room ID. If the room is missing
// this returns a 0 element slice.
func GetTimelineEventIDs(topLevelSyncJSON gjson.Result, roomID string) []string {
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
// if an item is not present, an error is returned.
// if an item not present in the want list is present, an error is returned.
// Items are compared using match.JSONDeepEqual
func CheckOffAll(items []interface{}, wantItems []interface{}) error {
	remaining, err := CheckOffAllAllowUnwanted(items, wantItems)
	if err != nil {
		return err
	}
	if len(remaining) > 0 {
		return fmt.Errorf("CheckOffAll: unexpected items %v", remaining)
	}
	return nil
}

// EXPERIMENTAL
// CheckOffAllAllowUnwanted checks that a list contains all of the given items, in any order.
// The updated list with the matched items removed from it is returned.
//
// if an item is not present, an error is returned
// Items are compared using match.JSONDeepEqual
func CheckOffAllAllowUnwanted(items []interface{}, wantItems []interface{}) ([]interface{}, error) {
	var err error
	for _, wantItem := range wantItems {
		items, err = CheckOff(items, wantItem)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

// EXPERIMENTAL
// CheckOff an item from the list. If the item is not present an error is returned
// The updated list with the matched item removed from it is returned. Items are
// compared using JSON deep equal.
func CheckOff(items []interface{}, wantItem interface{}) ([]interface{}, error) {
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
		return nil, fmt.Errorf("CheckOff: item %s not present", wantItem)
	}
	// delete the wanted item
	items = append(items[:want], items[want+1:]...)
	return items, nil
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
