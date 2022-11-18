package docker

import (
	"testing"
	"time"

	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/config"
)

// Deployment is the complete instantiation of a Blueprint, with running containers
// for each homeserver in the Blueprint.
type Deployment struct {
	// The Deployer which was responsible for this deployment
	Deployer *Deployer
	// The name of the deployed blueprint
	BlueprintName string
	// A map of HS name to a HomeserverDeployment
	HS     map[string]*HomeserverDeployment
	Config *config.Complement
}

// HomeserverDeployment represents a running homeserver in a container.
type HomeserverDeployment struct {
	BaseURL             string            // e.g http://localhost:38646
	FedBaseURL          string            // e.g https://localhost:48373
	ContainerID         string            // e.g 10de45efba
	AccessTokens        map[string]string // e.g { "@alice:hs1": "myAcc3ssT0ken" }
	ApplicationServices map[string]string // e.g { "my-as-id": "id: xxx\nas_token: xxx ..."} }
	DeviceIDs           map[string]string // e.g { "@alice:hs1": "myDeviceID" }
	CSAPIClients        []*client.CSAPI
}

// Updates the client and federation base URLs of the homeserver deployment.
func (hsDep *HomeserverDeployment) SetEndpoints(baseURL string, fedBaseURL string) {
	hsDep.BaseURL = baseURL
	hsDep.FedBaseURL = fedBaseURL

	for _, client := range hsDep.CSAPIClients {
		client.BaseURL = baseURL
	}
}

// Destroy the entire deployment. Destroys all running containers. If `printServerLogs` is true,
// will print container logs before killing the container.
func (d *Deployment) Destroy(t *testing.T) {
	t.Helper()
	d.Deployer.Destroy(d, d.Deployer.config.AlwaysPrintServerLogs || t.Failed())
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
	deviceID := dep.DeviceIDs[userID]
	if deviceID == "" && userID != "" {
		t.Logf("WARNING: Deployment.Client - HS name '%s' - user ID '%s' - deviceID not found", hsName, userID)
	}
	client := &client.CSAPI{
		UserID:           userID,
		AccessToken:      token,
		DeviceID:         deviceID,
		BaseURL:          dep.BaseURL,
		Client:           client.NewLoggedClient(t, hsName, nil),
		SyncUntilTimeout: 5 * time.Second,
		Debug:            d.Deployer.debugLogging,
	}
	dep.CSAPIClients = append(dep.CSAPIClients, client)
	return client
}

// NewUser creates a new user as a convenience method to RegisterUser.
//
//It registers the user with a deterministic password, and without admin privileges.
func (d *Deployment) NewUser(t *testing.T, localpart, hs string) *client.CSAPI {
	return d.RegisterUser(t, hs, localpart, "complement_meets_min_pasword_req_"+localpart, false)
}

// RegisterUser within a homeserver and return an authenticatedClient, Fails the test if the hsName is not found.
func (d *Deployment) RegisterUser(t *testing.T, hsName, localpart, password string, isAdmin bool) *client.CSAPI {
	t.Helper()
	dep, ok := d.HS[hsName]
	if !ok {
		t.Fatalf("Deployment.Client - HS name '%s' not found", hsName)
		return nil
	}
	client := &client.CSAPI{
		BaseURL:          dep.BaseURL,
		Client:           client.NewLoggedClient(t, hsName, nil),
		SyncUntilTimeout: 5 * time.Second,
		Debug:            d.Deployer.debugLogging,
	}
	dep.CSAPIClients = append(dep.CSAPIClients, client)
	var userID, accessToken, deviceID string
	if isAdmin {
		userID, accessToken, deviceID = client.RegisterSharedSecret(t, localpart, password, isAdmin)
	} else {
		userID, accessToken, deviceID = client.RegisterUser(t, localpart, password)
	}

	// remember the token so subsequent calls to deployment.Client return the user
	dep.AccessTokens[userID] = accessToken

	client.UserID = userID
	client.AccessToken = accessToken
	client.DeviceID = deviceID
	return client
}

// Restart a deployment.
func (d *Deployment) Restart(t *testing.T) error {
	t.Helper()
	for _, hsDep := range d.HS {
		err := d.Deployer.Restart(hsDep, d.Config)
		if err != nil {
			t.Errorf("Deployment.Restart: %s", err)
			return err
		}
	}

	return nil
}
