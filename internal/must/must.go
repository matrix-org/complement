// Package must contains assertions for tests
package must

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"reflect"
	"testing"

	"github.com/matrix-org/complement/internal/docker"
	"github.com/tidwall/gjson"
)

// NewDeployer will create a Deployer or terminate the test.
func NewDeployer(t *testing.T, namespace string) *docker.Deployer {
	t.Helper()
	d, err := docker.NewDeployer(namespace)
	if err != nil {
		t.Fatalf("MustNewDeployer: returned error %s", err)
	}
	return d
}

// Deploy will deploy the given blueprint in the given namespace or terminate the test.
func Deploy(t *testing.T, namespace, blueprintName string) *docker.Deployment {
	t.Helper()
	d := NewDeployer(t, namespace)
	dep, err := d.Deploy(context.Background(), blueprintName)
	if err != nil {
		t.Fatalf("MustDeploy: returned error %s", err)
	}
	return dep
}

// NotError will ensure `err` is nil else terminate the test with `msg`.
func NotError(t *testing.T, msg string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("MustNotError: %s -> %s", msg, err)
	}
}

// ParseJSON will ensure that the HTTP response body is valid JSON, then return the body, else terminate the test.
func ParseJSON(t *testing.T, res *http.Response) []byte {
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

// HaveJSONKey ensures that `wantKey` exists in `body`, and calls `checkFn` for additional testing.
// The format of `wantKey` is specified at https://godoc.org/github.com/tidwall/gjson#Get
// Terminates the test if the key is missing or if `checkFn` returns an error.
func HaveJSONKey(t *testing.T, body []byte, wantKey string, checkFn func(r gjson.Result) error) {
	t.Helper()
	res := gjson.GetBytes(body, wantKey)
	if res.Index == 0 {
		t.Fatalf("MustHaveJSONKey: key '%s' missing from %s", wantKey, string(body))
	}
	err := checkFn(res)
	if err != nil {
		t.Fatalf("MustHaveJSONKey: checkFn returned error: %s", err)
	}
	return
}

// HaveJSONKeyEqual ensures that `wantKey` exists in `body` and has the value `wantValue`, else terminates the test.
// The value is checked deeply via `reflect.DeepEqual`, and the got JSON value is mapped according to https://godoc.org/github.com/tidwall/gjson#Result.Value
func HaveJSONKeyEqual(t *testing.T, body []byte, wantKey string, wantValue interface{}) {
	t.Helper()
	HaveJSONKey(t, body, wantKey, func(r gjson.Result) error {
		gotValue := r.Value()
		if !reflect.DeepEqual(gotValue, wantValue) {
			return fmt.Errorf("MustHaveJSONKeyEqual: key '%s' got '%v' want '%v'", wantKey, gotValue, wantValue)
		}
		return nil
	})
}

// HaveStatus will ensure that the HTTP response has the desired status code or terminate the test.
func HaveStatus(t *testing.T, res *http.Response, wantStatusCode int) {
	t.Helper()
	if res.StatusCode != wantStatusCode {
		b, err := ioutil.ReadAll(res.Body)
		var body string
		if err != nil {
			body = err.Error()
		} else {
			body = string(b)
		}
		t.Fatalf("MustHaveStatus: %s got %d want %d - body: %s", res.Request.URL.String(), res.StatusCode, wantStatusCode, body)
	}
}

// HaveHeader will ensure that the HTTP response has the header `header` with the value `want` or terminate the test.
func HaveHeader(t *testing.T, res *http.Response, header string, want string) {
	t.Helper()
	got := res.Header.Get(header)
	if got != want {
		t.Fatalf("MustHaveHeader: [%s] got %s want %s", header, got, want)
	}
}

// EqualStr ensures that got==want else logs an error.
func EqualStr(t *testing.T, got, want, msg string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got '%s' want '%s'", msg, got, want)
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
