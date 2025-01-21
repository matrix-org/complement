package csapi_tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/must"
)

func TestProfileDisplayName(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	unauthedClient := deployment.UnauthenticatedClient(t, "hs1")
	authedClient := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	displayName := "my_display_name"
	// sytest: PUT /profile/:user_id/displayname sets my name
	t.Run("PUT /profile/:user_id/displayname sets my name", func(t *testing.T) {
		authedClient.MustSetDisplayName(t, displayName)
	})
	// sytest: GET /profile/:user_id/displayname publicly accessible
	t.Run("GET /profile/:user_id/displayname publicly accessible", func(t *testing.T) {
		gotDisplayName := unauthedClient.MustGetDisplayName(t, authedClient.UserID)
		must.Equal(t, gotDisplayName, displayName, "display name mismatch")
	})
}
