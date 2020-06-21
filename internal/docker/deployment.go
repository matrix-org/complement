package docker

import (
	"crypto/tls"
	"net/http"
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
	BaseURL      string            // e.g http://localhost:38646
	FedBaseURL   string            // e.g https://localhost:48373
	ContainerID  string            // e.g 10de45efba
	AccessTokens map[string]string // e.g { "@alice:hs1": "myAcc3ssT0ken" }
}

// Destroy the entire deployment. Destroys all running containers. If `printServerLogs` is true,
// will print container logs before killing the container.
func (d *Deployment) Destroy(t *testing.T) {
	t.Helper()
	d.Deployer.Destroy(d, t.Failed())
}

// Client returns a CSAPI client targetting the given hsName, using the access token for the given userID.
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
	}
}

// FederationClient return a SSAPI client targetting the given hsName.
// Fails the test if the hsName is not found.
func (d *Deployment) FederationClient(t *testing.T, hsName string) *client.Federation {
	t.Helper()
	dep, ok := d.HS[hsName]
	if !ok {
		t.Fatalf("Deployment.FederationClient - HS name '%s' not found", hsName)
		return nil
	}
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			ServerName:         hsName,
			InsecureSkipVerify: true,
		},
	}
	fedClient := &http.Client{
		Timeout:   30 * time.Second,
		Transport: tr,
	}
	fedClient.Transport = &RoundTripper{d}

	return &client.Federation{
		BaseURL: dep.FedBaseURL,
		Client:  client.NewLoggedClient(t, fedClient),
		HSName:  hsName,
	}
}
