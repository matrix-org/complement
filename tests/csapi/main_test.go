package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/internal/docker"
)

func TestMain(m *testing.M) {
	helpers.TestMain(m, "csapi")
}

func Deploy(t *testing.T, blueprint b.Blueprint) *docker.Deployment {
	t.Helper()
	return helpers.Deploy(t, blueprint)
}
