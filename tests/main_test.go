package tests

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/config"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/sirupsen/logrus"
)

// TestMain is the main entry point for Complement.
//
// It will clean up any old containers/images/networks from the previous run, then run the tests, then clean up
// again. No blueprints are made at this point as they are lazily made on demand.
func TestMain(m *testing.M) {
	cfg := config.NewConfigFromEnvVars()
	log.Printf("config: %+v", cfg)
	builder, err := docker.NewBuilder(cfg)
	if err != nil {
		fmt.Printf("Error: %s", err)
		os.Exit(1)
	}
	// remove any old images/containers/networks in case we died horribly before
	builder.Cleanup()
	// we use GMSL which uses logrus by default. We don't want those logs in our test output unless they are Serious.
	logrus.SetLevel(logrus.ErrorLevel)
	exitCode := m.Run()
	builder.Cleanup()
	os.Exit(exitCode)
}

// Deploy will deploy the given blueprint in the given namespace or terminate the test.
// It will construct the blueprint if it doesn't already exist in the docker image cache.
// This function is the main setup function for all tests as it provides a deployment with
// which tests can interact with.
func Deploy(t *testing.T, namespace string, blueprint b.Blueprint) *docker.Deployment {
	t.Helper()
	cfg := config.NewConfigFromEnvVars()
	builder, err := docker.NewBuilder(cfg)
	if err != nil {
		t.Fatalf("Deploy: docker.NewBuilder returned error: %s", err)
	}
	if err = builder.ConstructBlueprintsIfNotExist([]b.Blueprint{blueprint}); err != nil {
		t.Fatalf("Deploy: Failed to construct blueprint: %s", err)
	}
	d, err := docker.NewDeployer(namespace, cfg.DebugLoggingEnabled)
	if err != nil {
		t.Fatalf("Deploy: NewDeployer returned error %s", err)
	}
	dep, err := d.Deploy(context.Background(), blueprint.Name)
	if err != nil {
		t.Fatalf("Deploy: Deploy returned error %s", err)
	}
	return dep
}

// ExpectRouteToBeCalled `n` times. Returns a wait function which, when invoked, will block until either the timeout
// is reached or the route is called `n` times. Any invocations of the route after calling this function but
// before calling the wait function /are/ counted.
//
// Routes are counted via a channel, so if there are more than `n` invocations of the route /before/ the wait function
// is called then the handler will block.
func ExpectRouteToBeCalled(t *testing.T, n int, route *mux.Route) func(timeout time.Duration) {
	t.Helper()
	h := route.GetHandler()
	if h == nil {
		t.Fatalf("ExpectRouteToBeCalled: route has no handler")
	}
	ch := make(chan bool, n)

	// wrap the handler to send on the channel
	route.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer func() {
			ch <- true
		}()
		h.ServeHTTP(w, req)
	})

	count := 0
	return func(timeout time.Duration) {
		t.Helper()
		end := time.Now().Add(timeout)
		for count < n {
			now := time.Now()
			if now.After(end) {
				t.Fatalf("Wait: timed out after %f seconds. Route called %d times", timeout.Seconds(), count)
			}
			waitTime := end.Sub(now)
			select {
			case <-ch:
				count++
			case <-time.After(waitTime):
			}
		}
	}
}
