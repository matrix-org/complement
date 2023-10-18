package complement

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/internal/config"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/sirupsen/logrus"
)

// Deployment provides a way for tests to interact with a set of homeservers.
type Deployment interface {
	// UnauthenticatedClient returns a blank CSAPI client.
	UnauthenticatedClient(t *testing.T, serverName string) *client.CSAPI
	// Register a new user on the given server.
	Register(t *testing.T, hsName string, opts helpers.RegistrationOpts) *client.CSAPI
	// Login to an existing user account on the given server. In order to make tests not hardcode full user IDs,
	// an existing logged in client must be supplied.
	Login(t *testing.T, hsName string, existing *client.CSAPI, opts helpers.LoginOpts) *client.CSAPI
	// AppServiceUser returns a client for the given app service user ID. The HS in question must have an appservice
	// hooked up to it already. TODO: REMOVE
	AppServiceUser(t *testing.T, hsName, appServiceUserID string) *client.CSAPI
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

	// pointers to existing deployments for Deploy(t, 1) style deployments which are reused when run
	// in dirty mode.
	existingDeployments   map[int]*docker.Deployment
	existingDeploymentsMu *sync.Mutex
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
		complementBuilder:     builder,
		namespaceCounter:      0,
		Config:                cfg,
		existingDeployments:   make(map[int]*docker.Deployment),
		existingDeploymentsMu: &sync.Mutex{},
	}, nil
}

func (tp *TestPackage) Cleanup() {
	// any dirty deployments need logs printed and post scripts run
	tp.existingDeploymentsMu.Lock()
	for _, dep := range tp.existingDeployments {
		dep.DestroyAtCleanup()
	}
	tp.existingDeploymentsMu.Unlock()
	tp.complementBuilder.Cleanup()
}

// Deploy will deploy the given blueprint or terminate the test.
// It will construct the blueprint if it doesn't already exist in the docker image cache.
// This function is the main setup function for all tests as it provides a deployment with
// which tests can interact with.
func (tp *TestPackage) OldDeploy(t *testing.T, blueprint b.Blueprint) Deployment {
	t.Helper()
	timeStartBlueprint := time.Now()
	if err := tp.complementBuilder.ConstructBlueprintIfNotExist(blueprint); err != nil {
		t.Fatalf("OldDeploy: Failed to construct blueprint: %s", err)
	}
	namespace := fmt.Sprintf("%d", atomic.AddUint64(&tp.namespaceCounter, 1))
	d, err := docker.NewDeployer(namespace, tp.complementBuilder.Config)
	if err != nil {
		t.Fatalf("OldDeploy: NewDeployer returned error %s", err)
	}
	timeStartDeploy := time.Now()
	dep, err := d.Deploy(context.Background(), blueprint.Name)
	if err != nil {
		t.Fatalf("OldDeploy: Deploy returned error %s", err)
	}
	t.Logf("OldDeploy times: %v blueprints, %v containers", timeStartDeploy.Sub(timeStartBlueprint), time.Since(timeStartDeploy))
	return dep
}

func (tp *TestPackage) Deploy(t *testing.T, numServers int) Deployment {
	t.Helper()
	if tp.Config.EnableDirtyRuns {
		tp.existingDeploymentsMu.Lock()
		existingDep := tp.existingDeployments[numServers]
		tp.existingDeploymentsMu.Unlock()
		if existingDep != nil {
			return existingDep
		}
	}
	blueprint := mapServersToBlueprint(numServers)
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
	if tp.Config.EnableDirtyRuns {
		dep.Dirty = true // stop this deployment being destroyed.
		tp.existingDeploymentsMu.Lock()
		tp.existingDeployments[numServers] = dep
		tp.existingDeploymentsMu.Unlock()
	}
	return dep
}

// converts the requested number of servers into a single blueprint, which can be deployed using normal blueprint machinery.
func mapServersToBlueprint(numServers int) b.Blueprint {
	servers := make([]b.Homeserver, numServers)
	for i := range servers {
		servers[i] = b.Homeserver{
			Name: fmt.Sprintf("hs%d", i+1), // hs1,hs2,...
		}
	}
	return b.MustValidate(b.Blueprint{
		Name:        fmt.Sprintf("%d_servers", numServers),
		Homeservers: servers,
	})
}
