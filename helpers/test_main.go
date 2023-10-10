package helpers

import (
	"fmt"
	"os"
	"testing"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/internal/docker"
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
	var err error
	testPackage, err = NewTestPackage(namespace)
	if err != nil {
		fmt.Printf("Error: %s", err)
		os.Exit(1)
	}
	exitCode := m.Run()
	testPackage.Cleanup()
	os.Exit(exitCode)
}

// Deploy will deploy the given blueprint or terminate the test.
// It will construct the blueprint if it doesn't already exist in the docker image cache.
// This function is the main setup function for all tests as it provides a deployment with
// which tests can interact with.
func Deploy(t *testing.T, blueprint b.Blueprint) *docker.Deployment {
	t.Helper()
	return testPackage.Deploy(t, blueprint)
}
