package tests

import (
	"testing"

	"github.com/matrix-org/complement"
)

// TestMain runs the MSC4186 Complement test package.
func TestMain(m *testing.M) {
	complement.TestMain(m, "msc4186")
}
