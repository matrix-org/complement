package complement

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/config"
	"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/sirupsen/logrus"
)

// Deployment provides a way for tests to interact with a set of homeservers.
type Deployment interface {
	// Returns the resolvable host name of a homeserver given its short alias (e.g.,
	// "hs1", "hs2").
	//
	// In the case of the standard Docker deployment, this will be the same `hs1`, `hs2`
	// but may be different for other custom deployments.
	GetFullyQualifiedHomeserverName(hsName string) (string, error)
	// UnauthenticatedClient returns a blank CSAPI client.
	UnauthenticatedClient(t ct.TestLike, serverName string) *client.CSAPI
	// Register a new user on the given server.
	Register(t ct.TestLike, hsName string, opts helpers.RegistrationOpts) *client.CSAPI
	// Login to an existing user account on the given server. In order to make tests not hardcode full user IDs,
	// an existing logged in client must be supplied.
	Login(t ct.TestLike, hsName string, existing *client.CSAPI, opts helpers.LoginOpts) *client.CSAPI
	// AppServiceUser returns a client for the given app service user ID. The HS in question must have an appservice
	// hooked up to it already. TODO: REMOVE
	AppServiceUser(t ct.TestLike, hsName, appServiceUserID string) *client.CSAPI
	// Restart a deployment. Restarts all homeservers in this deployment.
	// This function is designed to be used to make assertions that servers are persisting information to disk.
	Restart(t ct.TestLike) error
	// Stop the container running this HS. Fails the test if this is not possible.
	// This function is designed to be used to make assertions when federated servers are unreachable.
	// Do not use this function if you need the HS CSAPI URL to be stable, prefer PauseServer if you need this.
	StopServer(t ct.TestLike, hsName string)
	// Start the container running this HS. The HS must exist in this deployment already and it must be stopped already.
	// Fails the test if this isn't true, or there was a problem.
	// This function is designed to be used to start a server previously stopped via StopServer.
	// Do not use this function if you need the HS CSAPI URL to be stable, prefer UnpauseServer if you need this.
	StartServer(t ct.TestLike, hsName string)
	// Pause a running homeserver. The HS will be suspended, preserving data in memory.
	// Prefer this function over StopServer if you need to keep the port allocations stable across your test.
	// This function is designed to be used to make assertions when federated servers are unreachable.
	// Fails the test if there is a problem pausing.
	// See https://docs.docker.com/engine/reference/commandline/pause/
	PauseServer(t ct.TestLike, hsName string)
	// Unpause a running homeserver. The HS will be suspended, preserving data in memory.
	// Fails the test if there is a problem unpausing.
	// This function is designed to be used to make assertions when federated servers are unreachable.
	// see https://docs.docker.com/engine/reference/commandline/unpause/
	UnpauseServer(t ct.TestLike, hsName string)
	// ContainerID returns the container ID of the given HS. Fails the test if there is no container for the given
	// HS name. This function is useful if you want to interact with the HS from the container runtime e.g to extract
	// logs (docker logs), check memory consumption (docker stats),
	ContainerID(t ct.TestLike, hsName string) string
	// Destroy the entire deployment. Destroys all running containers. If `printServerLogs` is true,
	// will print container logs before killing the container.
	Destroy(t ct.TestLike)
	// Return the complement config current active for this deployment
	GetConfig() *config.Complement
	// Return an HTTP round tripper interface which can map HS names to the actual container:port
	RoundTripper() http.RoundTripper
	// Return the network name if you want to attach additional containers to this network
	Network() string
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
	existingDeployment   *docker.Deployment
	existingDeploymentMu *sync.Mutex
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
		complementBuilder:    builder,
		namespaceCounter:     0,
		Config:               cfg,
		existingDeploymentMu: &sync.Mutex{},
	}, nil
}

func (tp *TestPackage) Cleanup() {
	// any dirty deployments need logs printed and post scripts run
	tp.existingDeploymentMu.Lock()
	if tp.existingDeployment != nil {
		tp.existingDeployment.DestroyAtCleanup()
	}
	tp.existingDeploymentMu.Unlock()
	tp.complementBuilder.Cleanup()
}

// Deploy will deploy the given blueprint or terminate the test.
// It will construct the blueprint if it doesn't already exist in the docker image cache.
// This function is the main setup function for all tests as it provides a deployment with
// which tests can interact with.
func (tp *TestPackage) OldDeploy(t ct.TestLike, blueprint b.Blueprint) Deployment {
	t.Helper()
	timeStartBlueprint := time.Now()
	if err := tp.complementBuilder.ConstructBlueprintIfNotExist(blueprint); err != nil {
		ct.Fatalf(t, "OldDeploy: Failed to construct blueprint: %s", err)
	}
	namespace := fmt.Sprintf("%d", atomic.AddUint64(&tp.namespaceCounter, 1))
	d, err := docker.NewDeployer(namespace, tp.complementBuilder.Config)
	if err != nil {
		ct.Fatalf(t, "OldDeploy: NewDeployer returned error %s", err)
	}
	timeStartDeploy := time.Now()
	dep, err := d.Deploy(context.Background(), blueprint.Name)
	if err != nil {
		ct.Fatalf(t, "OldDeploy: Deploy returned error %s", err)
	}
	t.Logf("OldDeploy times: %v blueprints, %v containers", timeStartDeploy.Sub(timeStartBlueprint), time.Since(timeStartDeploy))
	return dep
}

func (tp *TestPackage) Deploy(t ct.TestLike, numServers int) Deployment {
	t.Helper()
	if tp.Config.EnableDirtyRuns {
		return tp.dirtyDeploy(t, numServers)
	}
	// non-dirty deployments below
	blueprint := mapServersToBlueprint(numServers)
	timeStartBlueprint := time.Now()
	if err := tp.complementBuilder.ConstructBlueprintIfNotExist(blueprint); err != nil {
		ct.Fatalf(t, "Deploy: Failed to construct blueprint: %s", err)
	}
	namespace := fmt.Sprintf("%d", atomic.AddUint64(&tp.namespaceCounter, 1))
	d, err := docker.NewDeployer(namespace, tp.complementBuilder.Config)
	if err != nil {
		ct.Fatalf(t, "Deploy: NewDeployer returned error %s", err)
	}
	timeStartDeploy := time.Now()
	dep, err := d.Deploy(context.Background(), blueprint.Name)
	if err != nil {
		ct.Fatalf(t, "Deploy: Deploy returned error %s", err)
	}
	t.Logf("Deploy times: %v blueprints, %v containers", timeStartDeploy.Sub(timeStartBlueprint), time.Since(timeStartDeploy))
	return dep
}

func (tp *TestPackage) dirtyDeploy(t ct.TestLike, numServers int) Deployment {
	tp.existingDeploymentMu.Lock()
	defer tp.existingDeploymentMu.Unlock()
	// do we even have a deployment?
	if tp.existingDeployment == nil {
		d, err := docker.NewDeployer("dirty", tp.complementBuilder.Config)
		if err != nil {
			ct.Fatalf(t, "dirtyDeploy: NewDeployer returned error %s", err)
		}
		// this creates a single hs1
		tp.existingDeployment, err = d.CreateDirtyDeployment()
		if err != nil {
			ct.Fatalf(t, "CreateDirtyDeployment failed: %s", err)
		}
	}

	// if we have an existing deployment, can we use it? We can use it if we have at least that number of servers deployed already.
	if len(tp.existingDeployment.HS) >= numServers {
		return tp.existingDeployment
	}

	// we need to scale up the dirty deployment to more servers
	d, err := docker.NewDeployer("dirty", tp.complementBuilder.Config)
	if err != nil {
		ct.Fatalf(t, "dirtyDeploy: NewDeployer returned error %s", err)
	}
	for i := 1; i <= numServers; i++ {
		hsName := fmt.Sprintf("hs%d", i)
		_, ok := tp.existingDeployment.HS[hsName]
		if ok {
			continue
		}
		// scale up
		hsDep, err := d.CreateDirtyServer(hsName)
		if err != nil {
			ct.Fatalf(t, "dirtyDeploy: failed to add %s: %s", hsName, err)
		}
		tp.existingDeployment.HS[hsName] = hsDep
	}

	return tp.existingDeployment
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
