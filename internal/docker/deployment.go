package docker

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/internal/config"
	"github.com/matrix-org/gomatrixserverlib"
)

// Deployment is the complete instantiation of a Blueprint, with running containers
// for each homeserver in the Blueprint.
type Deployment struct {
	// The Deployer which was responsible for this deployment
	Deployer *Deployer
	// The name of the deployed blueprint
	BlueprintName string
	// Set to true if this deployment is a dirty deployment and so should not be destroyed.
	Dirty bool
	// A map of HS name to a HomeserverDeployment
	HS               map[string]*HomeserverDeployment
	Config           *config.Complement
	localpartCounter atomic.Int64
}

// HomeserverDeployment represents a running homeserver in a container.
type HomeserverDeployment struct {
	BaseURL             string            // e.g http://localhost:38646
	FedBaseURL          string            // e.g https://localhost:48373
	ContainerID         string            // e.g 10de45efba
	AccessTokens        map[string]string // e.g { "@alice:hs1": "myAcc3ssT0ken" }
	accessTokensMutex   sync.RWMutex
	ApplicationServices map[string]string // e.g { "my-as-id": "id: xxx\nas_token: xxx ..."} }
	DeviceIDs           map[string]string // e.g { "@alice:hs1": "myDeviceID" }

	// track all clients so if Restart() is called we can repoint to the new high-numbered port
	CSAPIClients      []*client.CSAPI
	CSAPIClientsMutex sync.Mutex
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
	if d.Dirty {
		if t.Failed() {
			d.Deployer.PrintLogs(d)
		}
		return
	}
	d.Deployer.Destroy(d, d.Deployer.config.AlwaysPrintServerLogs || t.Failed(), t.Name(), t.Failed())
}

func (d *Deployment) GetConfig() *config.Complement {
	return d.Config
}

func (d *Deployment) RoundTripper() http.RoundTripper {
	return &RoundTripper{Deployment: d}
}

func (d *Deployment) Register(t *testing.T, hsName string, opts helpers.RegistrationOpts) *client.CSAPI {
	dep, ok := d.HS[hsName]
	if !ok {
		t.Fatalf("Deployment.Register - HS name '%s' not found", hsName)
		return nil
	}
	client := &client.CSAPI{
		BaseURL:          dep.BaseURL,
		Client:           client.NewLoggedClient(t, hsName, nil),
		SyncUntilTimeout: 5 * time.Second,
		Debug:            d.Deployer.debugLogging,
	}
	// Appending a slice is not thread-safe. Protect the write with a mutex.
	dep.CSAPIClientsMutex.Lock()
	dep.CSAPIClients = append(dep.CSAPIClients, client)
	dep.CSAPIClientsMutex.Unlock()
	password := opts.Password
	if password == "" {
		password = "complement_meets_min_password_req"
	}

	localpart := fmt.Sprintf("user-%v", d.localpartCounter.Add(1))
	if opts.LocalpartSuffix != "" {
		localpart += fmt.Sprintf("-%s", opts.LocalpartSuffix)
	}
	var userID, accessToken, deviceID string
	if opts.IsAdmin {
		userID, accessToken, deviceID = client.RegisterSharedSecret(t, localpart, password, opts.IsAdmin)
	} else {
		userID, accessToken, deviceID = client.RegisterUser(t, localpart, password)
	}

	// remember the token so subsequent calls to deployment.Client return the user
	dep.accessTokensMutex.Lock()
	dep.AccessTokens[userID] = accessToken
	dep.accessTokensMutex.Unlock()

	client.UserID = userID
	client.AccessToken = accessToken
	client.DeviceID = deviceID
	return client
}

func (d *Deployment) Login(t *testing.T, hsName string, existing *client.CSAPI, opts helpers.LoginOpts) *client.CSAPI {
	t.Helper()
	dep, ok := d.HS[hsName]
	if !ok {
		t.Fatalf("Deployment.Login: HS name '%s' not found", hsName)
		return nil
	}
	localpart, _, err := gomatrixserverlib.SplitID('@', existing.UserID)
	if err != nil {
		t.Fatalf("Deployment.Login: existing CSAPI client has invalid user ID '%s', cannot login as this user: %s", existing.UserID, err)
	}
	client := &client.CSAPI{
		BaseURL:          dep.BaseURL,
		Client:           client.NewLoggedClient(t, hsName, nil),
		SyncUntilTimeout: 5 * time.Second,
		Debug:            d.Deployer.debugLogging,
	}
	// Appending a slice is not thread-safe. Protect the write with a mutex.
	dep.CSAPIClientsMutex.Lock()
	dep.CSAPIClients = append(dep.CSAPIClients, client)
	dep.CSAPIClientsMutex.Unlock()
	userID, accessToken, deviceID := client.LoginUser(t, localpart, opts.Password)

	client.UserID = userID
	client.AccessToken = accessToken
	client.DeviceID = deviceID
	return client
}

func (d *Deployment) UnauthenticatedClient(t *testing.T, hsName string) *client.CSAPI {
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
	// Appending a slice is not thread-safe. Protect the write with a mutex.
	dep.CSAPIClientsMutex.Lock()
	dep.CSAPIClients = append(dep.CSAPIClients, client)
	dep.CSAPIClientsMutex.Unlock()
	return client
}

// AppServiceUser returns a client for the given app service user ID. The HS in question must have an appservice
// hooked up to it already. TODO: REMOVE
func (d *Deployment) AppServiceUser(t *testing.T, hsName, appServiceUserID string) *client.CSAPI {
	t.Helper()
	dep, ok := d.HS[hsName]
	if !ok {
		t.Fatalf("Deployment.Client - HS name '%s' not found", hsName)
		return nil
	}
	dep.accessTokensMutex.RLock()
	token := dep.AccessTokens[appServiceUserID]
	dep.accessTokensMutex.RUnlock()
	if token == "" && appServiceUserID != "" {
		t.Fatalf("Deployment.Client - HS name '%s' - user ID '%s' not found", hsName, appServiceUserID)
		return nil
	}
	deviceID := dep.DeviceIDs[appServiceUserID]
	if deviceID == "" && appServiceUserID != "" {
		t.Logf("WARNING: Deployment.Client - HS name '%s' - user ID '%s' - deviceID not found", hsName, appServiceUserID)
	}
	client := &client.CSAPI{
		UserID:           appServiceUserID,
		AccessToken:      token,
		DeviceID:         deviceID,
		BaseURL:          dep.BaseURL,
		Client:           client.NewLoggedClient(t, hsName, nil),
		SyncUntilTimeout: 5 * time.Second,
		Debug:            d.Deployer.debugLogging,
	}
	// Appending a slice is not thread-safe. Protect the write with a mutex.
	dep.CSAPIClientsMutex.Lock()
	dep.CSAPIClients = append(dep.CSAPIClients, client)
	dep.CSAPIClientsMutex.Unlock()

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
