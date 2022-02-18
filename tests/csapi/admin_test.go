package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement/internal/b"
)

// Check if this homeserver supports Synapse-style admin registration.
// Not all images support this currently.
func TestCanRegisterAdmin(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	deployment.RegisterUser(t, "hs1", "admin", "adminpassword", true)
}
