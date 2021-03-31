package docker

import (
	"testing"
	"time"

	"github.com/matrix-org/complement/internal/client"
)

// Deployment is the complete instantiation of a Blueprint, with running containers
// for each homeserver in the Blueprint.
type Deployment struct {
	// The Deployer which was responsible for this deployment
	Deployer *Deployer
	// The name of the deployed blueprint
	BlueprintName string
	// A map of HS name to a HomeserverDeployment
	HS map[string]HomeserverDeployment
}

// HomeserverDeployment represents a running homeserver in a container.
type HomeserverDeployment struct {
	BaseURL             string            // e.g http://localhost:38646
	FedBaseURL          string            // e.g https://localhost:48373
	ContainerID         string            // e.g 10de45efba
	AccessTokens        map[string]string // e.g { "@alice:hs1": "myAcc3ssT0ken" }
	ApplicationServices map[string]string // e.g { "my-as-id": "id: xxx\nas_token: xxx ..."} }
}

// Destroy the entire deployment. Destroys all running containers. If `printServerLogs` is true,
// will print container logs before killing the container.
func (d *Deployment) Destroy(t *testing.T) {
	t.Helper()
	d.Deployer.Destroy(d, true) // t.Failed()
}

// Client returns a CSAPI client targeting the given hsName, using the access token for the given userID.
// Fails the test if the hsName is not found. Returns an unauthenticated client if userID is "", fails the test
// if the userID is otherwise not found.
func (d *Deployment) Client(t *testing.T, hsName, userID string) *client.CSAPI {
	t.Helper()
	dep, ok := d.HS[hsName]
	if !ok {
		t.Fatalf("Deployment.Client - HS name '%s' not found", hsName)
		return nil
	}
	token := dep.AccessTokens[userID]
	if token == "" && userID != "" {
		t.Fatalf("Deployment.Client - HS name '%s' - user ID '%s' not found", hsName, userID)
		return nil
	}
	return &client.CSAPI{
		UserID:           userID,
		AccessToken:      token,
		BaseURL:          dep.BaseURL,
		Client:           client.NewLoggedClient(t, nil),
		SyncUntilTimeout: 5 * time.Second,
		Debug:            d.Deployer.debugLogging,
	}
}
