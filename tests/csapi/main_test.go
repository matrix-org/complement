package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/internal/docker"
)

func TestMain(m *testing.M) {
	complement.TestMain(m, "csapi")
}

func Deploy(t *testing.T, blueprint b.Blueprint) *docker.Deployment {
	t.Helper()
	return complement.Deploy(t, blueprint)
}
