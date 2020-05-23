package tests

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"testing"

	"github.com/matrix-org/complement/internal"
	"github.com/matrix-org/complement/internal/config"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/tidwall/gjson"
)

// TestMain is the main entry point for Complement. It will process COMPLEMENT_ env vars and build blueprints
// to images before executing the tests.
func TestMain(m *testing.M) {
	cfg := config.NewConfigFromEnvVars()
	builder, err := internal.Start(cfg)
	if err != nil {
		fmt.Printf("Error: %s", err)
		os.Exit(1)
	}
	defer builder.Cleanup()
	os.Exit(m.Run())
}

// MustNewDeployer will create a Deployer or terminate the test.
func MustNewDeployer(t *testing.T, namespace string) *docker.Deployer {
	d, err := docker.NewDeployer(namespace)
	if err != nil {
		t.Fatalf("MustNewDeployer: returned error %s", err)
	}
	return d
}

// MustDeploy will deploy the given blueprint in the given namespace or terminate the test.
func MustDeploy(t *testing.T, namespace, blueprintName string) *docker.Deployment {
	d := MustNewDeployer(t, namespace)
	dep, err := d.Deploy(context.Background(), blueprintName)
	if err != nil {
		t.Fatalf("MustDeploy: returned error %s", err)
	}
	return dep
}

// MustNotError will ensure `err` is nil else terminate the test with `msg`.
func MustNotError(t *testing.T, msg string, err error) {
	if err != nil {
		t.Fatalf("MustNotError: %s -> %s", msg, err)
	}
}

// MustParseJSON will ensure that the HTTP response body is valid JSON, then return the body, else terminate the test.
func MustParseJSON(t *testing.T, res *http.Response) []byte {
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("MustParseJSON: reading HTTP response body returned %s", err)
	}
	if !gjson.ValidBytes(body) {
		t.Fatalf("MustParseJSON: Response is not valid JSON")
	}
	return body
}

// MustHaveJSONKey ensures that `wantKey` exists in `body`, and calls `checkFn` for additional testing.
// The format of `wantKey` is specified at https://godoc.org/github.com/tidwall/gjson#Get
// Terminates the test if the key is missing or if `checkFn` returns an error.
func MustHaveJSONKey(t *testing.T, body []byte, wantKey string, checkFn func(r gjson.Result) error) {
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

// MustHaveStatus will ensure that the HTTP response has the desired status code or terminate the test.
func MustHaveStatus(t *testing.T, res *http.Response, wantStatusCode int) {
	if res.StatusCode != wantStatusCode {
		t.Fatalf("MustHaveStatus: got %d want %d", res.StatusCode, wantStatusCode)
	}
}

// MustHaveHeader will ensure that the HTTP response has the header `header` with the value `want` or terminate the test.
func MustHaveHeader(t *testing.T, res *http.Response, header string, want string) {
	got := res.Header.Get(header)
	if got != want {
		t.Fatalf("MustHaveHeader: [%s] got %s want %s", header, got, want)
	}
}
