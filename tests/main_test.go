package tests

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/config"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/complement/internal/federation"
)

var namespaceCounter uint64

// persist the complement builder which is set when the tests start via TestMain
var complementBuilder *docker.Builder

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
	complementBuilder = builder
	// remove any old images/containers/networks in case we died horribly before
	builder.Cleanup()

	if os.Getenv("COMPLEMENT_CA") == "true" {
		log.Printf("Running with Complement CA")
		// make sure CA certs are generated
		_, _, err = federation.GetOrCreateCaCert()
		if err != nil {
			fmt.Printf("Error: %s", err)
			os.Exit(1)
		}
	}

	// we use GMSL which uses logrus by default. We don't want those logs in our test output unless they are Serious.
	logrus.SetLevel(logrus.ErrorLevel)

	exitCode := m.Run()
	builder.Cleanup()
	os.Exit(exitCode)
}

// A
// Deploy will deploy the given blueprint or terminate the test.
// It will construct the blueprint if it doesn't already exist in the docker image cache.
// This function is the main setup function for all tests as it provides a deployment with
// which tests can interact with.
func Deploy(t *testing.T, blueprint b.Blueprint) *docker.Deployment {
	t.Helper()
	timeStartBlueprint := time.Now()
	if complementBuilder == nil {
		t.Fatalf("complementBuilder not set, did you forget to call TestMain?")
	}
	if err := complementBuilder.ConstructBlueprintsIfNotExist([]b.Blueprint{blueprint}); err != nil {
		t.Fatalf("Deploy: Failed to construct blueprint: %s", err)
	}
	namespace := fmt.Sprintf("%d", atomic.AddUint64(&namespaceCounter, 1))
	d, err := docker.NewDeployer(namespace, complementBuilder.Config)
	if err != nil {
		t.Fatalf("Deploy: NewDeployer returned error %s", err)
	}
	timeStartDeploy := time.Now()
	dep, err := d.Deploy(context.Background(), blueprint.Name)
	if err != nil {
		t.Fatalf("Deploy: Deploy returned error %s", err)
	}
	ports := ""
	for hsName, d := range dep.HS {
		ports += fmt.Sprintf("%s=(%s,%s) ", hsName, d.BaseURL, d.FedBaseURL)
	}
	t.Logf("Deploy times: %v blueprints, %v containers - endpoints: %s", timeStartDeploy.Sub(timeStartBlueprint), time.Since(timeStartDeploy), ports)
	return dep
}

type Waiter struct {
	mu     sync.Mutex
	ch     chan bool
	closed bool
}

// NewWaiter returns a generic struct which can be waited on until `Waiter.Finish` is called.
// A Waiter is similar to a `sync.WaitGroup` of size 1, but without the ability to underflow and
// with built-in timeouts.
func NewWaiter() *Waiter {
	return &Waiter{
		ch: make(chan bool),
		mu: sync.Mutex{},
	}
}

// Wait blocks until Finish() is called or until the timeout is reached.
// If the timeout is reached, the test is failed.
func (w *Waiter) Wait(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-w.ch:
		return
	case <-time.After(timeout):
		t.Fatalf("Wait: timed out after %f seconds.", timeout.Seconds())
	}
}

// Finish will cause all goroutines waiting via Wait to stop waiting and return.
// Once this function has been called, subsequent calls to Wait will return immediately.
// To begin waiting again, make a new Waiter.
func (w *Waiter) Finish() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	close(w.ch)
}
