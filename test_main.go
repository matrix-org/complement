package complement

import (
	"fmt"
	"os"
	"testing"

	"github.com/matrix-org/complement/b"
)

var testPackage *TestPackage

// TestMain is the main entry point for Complement.
//
// It will clean up any old containers/images/networks from the previous run, then run the tests, then clean up
// again. No blueprints are made at this point as they are lazily made on demand.
//
// The 'namespace' should be unique for this test package, among all test packages which may run in parallel, to avoid
// docker containers stepping on each other. For MSCs, use the MSC name. For versioned releases, use the version number
// along with any sub-directory name.
func TestMain(m *testing.M, namespace string) {
	TestMainWithCleanup(m, namespace, nil)
}

// TestMainWithCleanup is TestMain but with a cleanup function prior to terminating the test suite.
// This function should be used for per-suite cleanup operations e.g tearing down containers, killing
// child processes, etc.
func TestMainWithCleanup(m *testing.M, namespace string, cleanup func()) {
	var err error
	testPackage, err = NewTestPackage(namespace)
	if err != nil {
		fmt.Printf("Error: %s", err)
		os.Exit(1)
	}
	exitCode := m.Run()
	testPackage.Cleanup()
	if cleanup != nil {
		cleanup()
	}
	os.Exit(exitCode)
}

// Deploy will deploy the given blueprint or terminate the test.
// It will construct the blueprint if it doesn't already exist in the docker image cache.
// This function is the main setup function for all tests as it provides a deployment with
// which tests can interact with.
func OldDeploy(t *testing.T, blueprint b.Blueprint) Deployment {
	t.Helper()
	if testPackage == nil {
		t.Fatalf("Deploy: testPackage not set, did you forget to call complement.TestMain?")
	}
	return testPackage.OldDeploy(t, blueprint)
}

// Deploy will deploy the given number of servers or terminate the test.
// This function is the main setup function for all tests as it provides a deployment with
// which tests can interact with.
func Deploy(t *testing.T, numServers int) Deployment {
	t.Helper()
	if testPackage == nil {
		t.Fatalf("Deploy: testPackage not set, did you forget to call complement.TestMain?")
	}
	return testPackage.Deploy(t, numServers)
}
