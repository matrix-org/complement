// Package must contains assertions for tests, which fail the test if the assertion fails.
package must

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/gomatrixserverlib/fclient"

	"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/should"
)

const ansiRedForeground = "\x1b[31m"
const ansiResetForeground = "\x1b[39m"

// NotError will ensure `err` is nil else terminate the test with `msg`.
func NotError(t ct.TestLike, msg string, err error) {
	t.Helper()
	if err != nil {
		ct.Fatalf(t, "must.NotError: %s -> %s", msg, err)
	}
}

// EXPERIMENTAL
// ParseJSON will ensure that the HTTP request/response body is valid JSON, then return the body, else terminate the test.
func ParseJSON(t ct.TestLike, b io.ReadCloser) gjson.Result {
	t.Helper()
	res, err := should.ParseJSON(b)
	if err != nil {
		ct.Fatalf(t, err.Error())
	}
	return res
}

// EXPERIMENTAL
// MatchRequest consumes the HTTP request and performs HTTP-level assertions on it. Returns the raw response body.
func MatchRequest(t ct.TestLike, req *http.Request, m match.HTTPRequest) []byte {
	t.Helper()
	res, err := should.MatchRequest(req, m)
	if err != nil {
		ct.Fatalf(t, err.Error())
	}
	return res
}

// EXPERIMENTAL
// MatchSuccess consumes the HTTP response and fails if the response is non-2xx.
func MatchSuccess(t ct.TestLike, res *http.Response) {
	t.Helper()
	if err := should.MatchSuccess(res); err != nil {
		ct.Fatalf(t, err.Error())
	}
}

// EXPERIMENTAL
// MatchFailure consumes the HTTP response and fails if the response is 2xx.
func MatchFailure(t ct.TestLike, res *http.Response) {
	t.Helper()
	if err := should.MatchFailure(res); err != nil {
		ct.Fatalf(t, err.Error())
	}
}

// EXPERIMENTAL
// MatchResponse consumes the HTTP response and performs HTTP-level assertions on it. Returns the raw response body.
func MatchResponse(t ct.TestLike, res *http.Response, m match.HTTPResponse) []byte {
	t.Helper()
	body, err := should.MatchResponse(res, m)
	if err != nil {
		ct.Fatalf(t, err.Error())
	}
	return body
}

// MatchFederationRequest performs JSON assertions on incoming federation requests.
func MatchFederationRequest(t ct.TestLike, fedReq *fclient.FederationRequest, matchers ...match.JSON) {
	t.Helper()
	err := should.MatchFederationRequest(fedReq)
	if err != nil {
		ct.Fatalf(t, err.Error())
	}
}

// EXPERIMENTAL
// MatchGJSON performs JSON assertions on a gjson.Result object.
func MatchGJSON(t ct.TestLike, jsonResult gjson.Result, matchers ...match.JSON) {
	t.Helper()
	err := should.MatchGJSON(jsonResult, matchers...)
	if err != nil {
		ct.Fatalf(t, err.Error())
	}
}

// EXPERIMENTAL
// MatchJSONBytes performs JSON assertions on a raw json byte slice.
func MatchJSONBytes(t ct.TestLike, rawJson []byte, matchers ...match.JSON) {
	t.Helper()
	err := should.MatchJSONBytes(rawJson, matchers...)
	if err != nil {
		ct.Fatalf(t, err.Error())
	}
}

// Equal ensures that got==want else logs an error.
// The 'msg' is displayed with the error to provide extra context.
func Equal[V comparable](t ct.TestLike, got, want V, msg string) {
	t.Helper()
	if got != want {
		ct.Errorf(t, "Equal %s: got '%v' want '%v'", msg, got, want)
	}
}

// NotEqual ensures that got!=want else logs an error.
// The 'msg' is displayed with the error to provide extra context.
func NotEqual[V comparable](t ct.TestLike, got, want V, msg string) {
	t.Helper()
	if got == want {
		ct.Errorf(t, "NotEqual %s: got '%v', want '%v'", msg, got, want)
	}
}

// EXPERIMENTAL
// StartWithStr ensures that got starts with wantPrefix else logs an error.
func StartWithStr(t ct.TestLike, got, wantPrefix, msg string) {
	t.Helper()
	if !strings.HasPrefix(got, wantPrefix) {
		ct.Errorf(t, "StartWithStr: %s: got '%s' without prefix '%s'", msg, got, wantPrefix)
	}
}

// GetJSONFieldStr extracts the string value under `wantKey` or fails the test.
// The format of `wantKey` is specified at https://godoc.org/github.com/tidwall/gjson#Get
func GetJSONFieldStr(t ct.TestLike, body gjson.Result, wantKey string) string {
	t.Helper()
	str, err := should.GetJSONFieldStr(body, wantKey)
	if err != nil {
		ct.Fatalf(t, err.Error())
	}
	return str
}

// EXPERIMENTAL
// HaveInOrder checks that the two slices match exactly, failing the test on mismatches or omissions.
func HaveInOrder[V comparable](t ct.TestLike, gots []V, wants []V) {
	t.Helper()
	err := should.HaveInOrder(gots, wants)
	if err != nil {
		ct.Fatalf(t, err.Error())
	}
}

// EXPERIMENTAL
// ContainSubset checks that every item in smaller is in larger, failing the test if at least 1 item isn't. Ignores extra elements
// in larger. Ignores ordering.
func ContainSubset[V comparable](t ct.TestLike, larger []V, smaller []V) {
	t.Helper()
	err := should.ContainSubset(larger, smaller)
	if err != nil {
		ct.Fatalf(t, err.Error())
	}
}

// EXPERIMENTAL
// NotContainSubset checks that every item in smaller is NOT in larger, failing the test if at least 1 item is. Ignores extra elements
// in larger. Ignores ordering.
func NotContainSubset[V comparable](t ct.TestLike, larger []V, smaller []V) {
	t.Helper()
	err := should.NotContainSubset(larger, smaller)
	if err != nil {
		ct.Fatalf(t, err.Error())
	}
}

// EXPERIMENTAL
// CheckOffAll checks that a list contains exactly the given items, in any order.
//
// if an item is not present, the test is failed.
// if an item not present in the want list is present, the test is failed.
// Items are compared using match.JSONDeepEqual
func CheckOffAll(t ct.TestLike, items []interface{}, wantItems []interface{}) {
	t.Helper()
	err := should.CheckOffAll(items, wantItems)
	if err != nil {
		ct.Fatalf(t, err.Error())
	}
}

// EXPERIMENTAL
// CheckOffAllAllowUnwanted checks that a list contains all of the given items, in any order.
// The updated list with the matched items removed from it is returned.
//
// if an item is not present, the test is failed.
// Items are compared using match.JSONDeepEqual
func CheckOffAllAllowUnwanted(t ct.TestLike, items []interface{}, wantItems []interface{}) []interface{} {
	t.Helper()
	result, err := should.CheckOffAllAllowUnwanted(items, wantItems)
	if err != nil {
		ct.Fatalf(t, err.Error())
	}
	return result
}

// EXPERIMENTAL
// CheckOff an item from the list. If the item is not present the test is failed.
// The updated list with the matched item removed from it is returned. Items are
// compared using JSON deep equal.
func CheckOff(t ct.TestLike, items []interface{}, wantItem interface{}) []interface{} {
	t.Helper()
	result, err := should.CheckOff(items, wantItem)
	if err != nil {
		ct.Fatalf(t, err.Error())
	}
	return result
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
