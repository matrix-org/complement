package complement

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/internal/config"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/sirupsen/logrus"
)

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
func (tp *TestPackage) Deploy(t *testing.T, blueprint b.Blueprint) *docker.Deployment {
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
