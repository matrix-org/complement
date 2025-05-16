package complement

import (
	"fmt"
	"os"
	"testing"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/config"
	"github.com/matrix-org/complement/ct"
)

var (
	testPackage    *TestPackage
	customDeployer func(t ct.TestLike, numServers int, config *config.Complement) Deployment
)

type complementOpts struct {
	// Args:
	// - We pass in the Complement config (`testPackage.Config`) so the deployer can inspect
	// `DebugLoggingEnabled`, `SpawnHSTimeout`, `PackageNamespace` etc.
	cleanup func(config *config.Complement)
	// Args:
	// - We pass in `t` as there needs to be a way to handle an error in the custom deployer.
	// - We pass in the Complement config (`testPackage.Config`) so the deployer can inspect
	// `DebugLoggingEnabled`, `SpawnHSTimeout`, `PackageNamespace`, etc.
	customDeployment func(t ct.TestLike, numServers int, config *config.Complement) Deployment
}
type opt func(*complementOpts)

// WithCleanup adds a cleanup function which is called prior to terminating the test suite.
// It is called BEFORE Complement containers are destroyed.
// This function should be used for per-suite cleanup operations e.g tearing down containers, killing
// child processes, etc.
func WithCleanup(fn func(config *config.Complement)) opt {
	return func(co *complementOpts) {
		co.cleanup = fn
	}
}

// WithDeployment adds a custom mechanism to deploy homeservers.
//
// For test consistency and compatibility, deployers should be creating servers that can
// be referred to as `hs1`, `hs2`, etc as the `hsName` in the `Deployment` interface.
// The actual resolvable address of the homeserver in the network can be something
// different and just needs to be mapped by
// your implementation of `deployment.GetFullyQualifiedHomeserverName(hsName)`.
func WithDeployment(fn func(t ct.TestLike, numServers int, config *config.Complement) Deployment) opt {
	return func(co *complementOpts) {
		co.customDeployment = fn
	}
}

// TestMain is the main entry point for Complement.
//
// It will clean up any old containers/images/networks from the previous run, then run the tests, then clean up
// again. No blueprints are made at this point as they are lazily made on demand.
//
// The 'namespace' should be unique for this test package, among all test packages which may run in parallel, to avoid
// docker containers stepping on each other. For MSCs, use the MSC name. For versioned releases, use the version number
// along with any sub-directory name.
//
// Functional options can be used to control how Complement processes deployments.
func TestMain(m *testing.M, namespace string, customOpts ...opt) {
	opts := &complementOpts{}
	for _, o := range customOpts {
		o(opts)
	}
	if opts.customDeployment != nil {
		customDeployer = opts.customDeployment
	}

	var err error
	testPackage, err = NewTestPackage(namespace)
	if err != nil {
		fmt.Printf("Error: %s", err)
		os.Exit(1)
	}
	exitCode := m.Run()
	if opts.cleanup != nil {
		opts.cleanup(testPackage.Config)
	}
	testPackage.Cleanup()
	os.Exit(exitCode)
}

// Deploy will deploy the given blueprint or terminate the test.
// It will construct the blueprint if it doesn't already exist in the docker image cache.
// This function is the main setup function for all tests as it provides a deployment with
// which tests can interact with.
func OldDeploy(t ct.TestLike, blueprint b.Blueprint) Deployment {
	t.Helper()
	if testPackage == nil {
		ct.Fatalf(t, "Deploy: testPackage not set, did you forget to call complement.TestMain?")
	}
	return testPackage.OldDeploy(t, blueprint)
}

// Deploy will deploy the given number of servers or terminate the test.
// This function is the main setup function for all tests as it provides a deployment with
// which tests can interact with.
//
// For test consistency and compatibility, deployers should be creating servers that can
// be referred to as `hs1`, `hs2`, etc as the `hsName` in the `Deployment` interface.
func Deploy(t ct.TestLike, numServers int) Deployment {
	t.Helper()
	if testPackage == nil {
		ct.Fatalf(t, "Deploy: testPackage not set, did you forget to call complement.TestMain?")
	}
	if customDeployer != nil {
		return customDeployer(t, numServers, testPackage.Config)
	}
	return testPackage.Deploy(t, numServers)
}
