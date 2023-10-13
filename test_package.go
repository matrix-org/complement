package complement

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/internal/config"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/sirupsen/logrus"
)

// Deployment provides a way for tests to interact with a set of homeservers.
type Deployment interface {
	// Client returns a CSAPI client targeting the given hsName, using the access token for the given userID.
	// Fails the test if the hsName is not found. Returns an unauthenticated client if userID is "", fails the test
	// if the userID is otherwise not found.
	Client(t *testing.T, serverName, userID string) *client.CSAPI
	// RegisterUser within a homeserver and return an authenticatedClient, Fails the test if the hsName is not found.
	RegisterUser(t *testing.T, hsName, localpart, password string, isAdmin bool) *client.CSAPI
	// NewUser creates a new user as a convenience method to RegisterUser. TODO REMOVE
	//
	// It registers the user with a deterministic password, and without admin privileges.
	NewUser(t *testing.T, localpart, hs string) *client.CSAPI
	// TODO remove this, only used in 1 test in msc3890
	// LoginUser within a homeserver and return an authenticatedClient. Fails the test if the hsName is not found.
	// Note that this will not change the access token of the client that is returned by `deployment.Client`.
	LoginUser(t *testing.T, hsName, localpart, password string) *client.CSAPI
	// Restart a deployment.
	Restart(t *testing.T) error
	// Destroy the entire deployment. Destroys all running containers. If `printServerLogs` is true,
	// will print container logs before killing the container.
	Destroy(t *testing.T)
	// Return the complement config current active for this deployment
	GetConfig() *config.Complement
	// Return an HTTP round tripper interface which can map HS names to the actual container:port
	RoundTripper() http.RoundTripper
}

// TestPackage represents the configuration for a package of tests. A package of tests
// are all tests in the same Go package (directory).
type TestPackage struct {
	// the config used for this package.
	Config *config.Complement
	// the builder we'll use to make containers
	complementBuilder *docker.Builder
	// a counter to stop tests from allocating the same container name
	namespaceCounter uint64
}

// NewTestPackage creates a new test package which can be used to deploy containers for all tests
// in a single package. This should be called from `TestMain` which is the Go-provided entry point
// before any tests run. After the tests have run, call `TestPackage.Cleanup`. Tests can deploy
// containers by calling `TestPackage.Deploy`.
func NewTestPackage(pkgNamespace string) (*TestPackage, error) {
	cfg := config.NewConfigFromEnvVars(pkgNamespace, "")
	log.Printf("config: %+v", cfg)
	builder, err := docker.NewBuilder(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to make docker builder: %w", err)
	}
	// remove any old images/containers/networks in case we died horribly before
	builder.Cleanup()

	// we use GMSL which uses logrus by default. We don't want those logs in our test output unless they are Serious.
	logrus.SetLevel(logrus.ErrorLevel)

	return &TestPackage{
		complementBuilder: builder,
		namespaceCounter:  0,
		Config:            cfg,
	}, nil
}

func (tp *TestPackage) Cleanup() {
	tp.complementBuilder.Cleanup()
}

// Deploy will deploy the given blueprint or terminate the test.
// It will construct the blueprint if it doesn't already exist in the docker image cache.
// This function is the main setup function for all tests as it provides a deployment with
// which tests can interact with.
func (tp *TestPackage) Deploy(t *testing.T, blueprint b.Blueprint) Deployment {
	t.Helper()
	timeStartBlueprint := time.Now()
	if err := tp.complementBuilder.ConstructBlueprintIfNotExist(blueprint); err != nil {
		t.Fatalf("Deploy: Failed to construct blueprint: %s", err)
	}
	namespace := fmt.Sprintf("%d", atomic.AddUint64(&tp.namespaceCounter, 1))
	d, err := docker.NewDeployer(namespace, tp.complementBuilder.Config)
	if err != nil {
		t.Fatalf("Deploy: NewDeployer returned error %s", err)
	}
	timeStartDeploy := time.Now()
	dep, err := d.Deploy(context.Background(), blueprint.Name)
	if err != nil {
		t.Fatalf("Deploy: Deploy returned error %s", err)
	}
	t.Logf("Deploy times: %v blueprints, %v containers", timeStartDeploy.Sub(timeStartBlueprint), time.Since(timeStartDeploy))
	return dep
}

func (tp *TestPackage) DeployDirty(t *testing.T, numServers int) Deployment {
	return nil
}
