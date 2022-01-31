// Copyright 2020 The Matrix.org Foundation C.I.C.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"time"

	"github.com/docker/docker/client"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"

	"github.com/matrix-org/complement/internal/config"
)

type Deployer struct {
	DeployNamespace string
	Docker          *client.Client
	Counter         int
	networkID       string
	debugLogging    bool
	config          *config.Complement
}

func NewDeployer(deployNamespace string, cfg *config.Complement) (*Deployer, error) {
	cli, err := client.NewEnvClient()
	if err != nil {
		return nil, err
	}
	return &Deployer{
		DeployNamespace: deployNamespace,
		Docker:          cli,
		debugLogging:    cfg.DebugLoggingEnabled,
		config:          cfg,
	}, nil
}

func (d *Deployer) log(str string, args ...interface{}) {
	if !d.debugLogging {
		return
	}
	log.Printf(str, args...)
}

func (d *Deployer) Deploy(ctx context.Context, blueprintName string) (*Deployment, error) {
	dep := &Deployment{
		Deployer:      d,
		BlueprintName: blueprintName,
		HS:            make(map[string]HomeserverDeployment),
		Config:        d.config,
	}
	images, err := d.Docker.ImageList(ctx, types.ImageListOptions{
		Filters: label(
			"complement_pkg="+d.config.PackageNamespace,
			"complement_blueprint="+blueprintName,
		),
	})
	if err != nil {
		return nil, fmt.Errorf("Deploy: failed to ImageList: %w", err)
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("Deploy: No images have been built for blueprint %s", blueprintName)
	}
	networkID, err := createNetworkIfNotExists(d.Docker, d.config.PackageNamespace, blueprintName)
	if err != nil {
		return nil, fmt.Errorf("Deploy: %w", err)
	}
	d.networkID = networkID

	// deploy images in parallel
	var mu sync.Mutex // protects mutable values like the counter and errors
	var wg sync.WaitGroup
	wg.Add(len(images)) // ensure we wait until all images have deployed
	deployImg := func(img types.ImageSummary) error {
		defer wg.Done()
		mu.Lock()
		d.Counter++
		counter := d.Counter
		mu.Unlock()
		contextStr := img.Labels["complement_context"]
		hsName := img.Labels["complement_hs_name"]
		asIDToRegistrationMap := asIDToRegistrationFromLabels(img.Labels)

		// TODO: Make CSAPI port configurable
		deployment, err := deployImage(
			d.Docker, img.ID, fmt.Sprintf("complement_%s_%s_%s_%d", d.config.PackageNamespace, d.DeployNamespace, contextStr, counter),
			d.config.PackageNamespace, blueprintName, hsName, asIDToRegistrationMap, contextStr, networkID, d.config,
		)
		if err != nil {
			if deployment != nil && deployment.ContainerID != "" {
				// print logs to help debug
				printLogs(d.Docker, deployment.ContainerID, contextStr)
			}
			return fmt.Errorf("Deploy: Failed to deploy image %+v : %w", img, err)
		}
		mu.Lock()
		d.log("%s -> %s (%s)\n", contextStr, deployment.BaseURL, deployment.ContainerID)
		dep.HS[hsName] = *deployment
		mu.Unlock()
		return nil
	}

	var lastErr error
	for _, img := range images {
		go func(i types.ImageSummary) {
			err := deployImg(i)
			if err != nil {
				mu.Lock()
				lastErr = err
				mu.Unlock()
			}
		}(img)
	}
	wg.Wait()
	return dep, lastErr
}

// Destroy a deployment. This will kill all running containers.
func (d *Deployer) Destroy(dep *Deployment, printServerLogs bool) {
	for _, hsDep := range dep.HS {
		if printServerLogs {
			printLogs(d.Docker, hsDep.ContainerID, hsDep.ContainerID)
		}
		err := d.Docker.ContainerKill(context.Background(), hsDep.ContainerID, "KILL")
		if err != nil {
			log.Printf("Destroy: Failed to destroy container %s : %s\n", hsDep.ContainerID, err)
		}
		err = d.Docker.ContainerRemove(context.Background(), hsDep.ContainerID, types.ContainerRemoveOptions{
			Force: true,
		})
		if err != nil {
			log.Printf("Destroy: Failed to remove container %s : %s\n", hsDep.ContainerID, err)
		}
	}
}

func deployImage(
	docker *client.Client, imageID string, containerName, pkgNamespace, blueprintName, hsName string,
	asIDToRegistrationMap map[string]string, contextStr, networkID string, cfg *config.Complement,
) (*HomeserverDeployment, error) {
	ctx := context.Background()
	var extraHosts []string
	var mounts []mount.Mount
	var err error

	if runtime.GOOS == "linux" {
		// By default docker for linux does not expose this, so do it now.
		// When https://github.com/moby/moby/pull/40007 lands in Docker 20, we should
		// change this to be  `host.docker.internal:host-gateway`
		extraHosts = []string{HostnameRunningComplement + ":172.17.0.1"}
	}

	toMount := []Volume{
		&VolumeAppService{},
	}

	for _, m := range toMount {
		err = m.Prepare(ctx, docker, contextStr)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare volume: %s", err)
		}
		mounts = append(mounts, m.Mount())
	}

	env := []string{
		"SERVER_NAME=" + hsName,
		// TODO: Remove once Synapse images don't rely on this anymore
		"COMPLEMENT_CA=1",
	}

	body, err := docker.ContainerCreate(ctx, &container.Config{
		Image: imageID,
		Env:   env,
		//Cmd:   d.ImageArgs,
		Labels: map[string]string{
			complementLabel:        contextStr,
			"complement_blueprint": blueprintName,
			"complement_pkg":       pkgNamespace,
			"complement_hs_name":   hsName,
		},
	}, &container.HostConfig{
		PublishAllPorts: true,
		ExtraHosts:      extraHosts,
		Mounts:          mounts,
	}, &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			hsName: {
				NetworkID: networkID,
				Aliases:   []string{hsName},
			},
		},
	}, containerName)
	if err != nil {
		return nil, err
	}

	containerID := body.ID
	if cfg.DebugLoggingEnabled {
		log.Printf("%s: Created container %s", contextStr, containerID)
	}

	// Create the application service files
	for asID, registration := range asIDToRegistrationMap {
		err = copyToContainer(docker, containerID, fmt.Sprintf("/appservices/%s.yaml", url.PathEscape(asID)), []byte(registration))
		if err != nil {
			return nil, err
		}
	}

	// Copy CA certificate and key
	certBytes, err := cfg.CACertificateBytes()
	if err != nil {
		return nil, fmt.Errorf("failed to get CA certificate: %s", err)
	}
	err = copyToContainer(docker, containerID, "/ca/ca.crt", certBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to copy CA certificate to container: %s", err)
	}
	certKeyBytes, err := cfg.CAPrivateKeyBytes()
	if err != nil {
		return nil, fmt.Errorf("failed to get CA key: %s", err)
	}
	err = copyToContainer(docker, containerID, "/ca/ca.key", certKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to copy CA key to container: %s", err)
	}

	err = docker.ContainerStart(ctx, containerID, types.ContainerStartOptions{})
	if err != nil {
		return nil, err
	}
	if cfg.DebugLoggingEnabled {
		log.Printf("%s: Started container %s", contextStr, containerID)
	}
	var inspect types.ContainerJSON
	inspect, err = docker.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, err
	}
	baseURL, fedBaseURL, err := endpoints(inspect.NetworkSettings.Ports, 8008, 8448)
	if err != nil {
		return nil, fmt.Errorf("%s : image %s : %w", contextStr, imageID, err)
	}
	var lastErr error

	// Inspect health status of container to check it is up
	stopTime := time.Now().Add(cfg.SpawnHSTimeout)
	iterCount := 0
	if inspect.State.Health != nil {
		// If the container has a healthcheck, wait for it first
		for {
			iterCount += 1
			if time.Now().After(stopTime) {
				lastErr = fmt.Errorf("timed out checking for homeserver to be up: %s", lastErr)
				break
			}
			inspect, err = docker.ContainerInspect(ctx, containerID)
			if err != nil {
				lastErr = fmt.Errorf("Inspect container %s => error: %s", containerID, err)
				time.Sleep(50 * time.Millisecond)
				continue
			}
			if inspect.State.Health.Status != "healthy" {
				lastErr = fmt.Errorf("Inspect container %s => health: %s", containerID, inspect.State.Health.Status)
				time.Sleep(50 * time.Millisecond)
				continue
			}
			lastErr = nil
			break

		}
	}

	// Having optionally waited for container to self-report healthy
	// hit /versions to check it is actually responding
	versionsURL := fmt.Sprintf("%s/_matrix/client/versions", baseURL)

	for {
		iterCount += 1
		if time.Now().After(stopTime) {
			lastErr = fmt.Errorf("timed out checking for homeserver to be up: %s", lastErr)
			break
		}
		res, err := http.Get(versionsURL)
		if err != nil {
			lastErr = fmt.Errorf("GET %s => error: %s", versionsURL, err)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if res.StatusCode != 200 {
			lastErr = fmt.Errorf("GET %s => HTTP %s", versionsURL, res.Status)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		lastErr = nil
		break
	}

	d := &HomeserverDeployment{
		BaseURL:             baseURL,
		FedBaseURL:          fedBaseURL,
		ContainerID:         containerID,
		AccessTokens:        tokensFromLabels(inspect.Config.Labels),
		ApplicationServices: asIDToRegistrationFromLabels(inspect.Config.Labels),
	}
	if lastErr != nil {
		return d, fmt.Errorf("%s: failed to check server is up. %w", contextStr, lastErr)
	} else {
		if cfg.DebugLoggingEnabled {
			log.Printf("%s: Server is responding after %d iterations", contextStr, iterCount)
		}
	}
	return d, nil
}

func copyToContainer(docker *client.Client, containerID, path string, data []byte) error {
	// Create a fake/virtual file in memory that we can copy to the container
	// via https://stackoverflow.com/a/52131297/796832
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	err := tw.WriteHeader(&tar.Header{
		Name: path,
		Mode: 0777,
		Size: int64(len(data)),
	})
	if err != nil {
		return fmt.Errorf("copyToContainer: failed to write tarball header for %s: %v", path, err)
	}
	tw.Write([]byte(data))
	tw.Close()

	// Put our new fake file in the container volume
	err = docker.CopyToContainer(context.Background(), containerID, "/", &buf, types.CopyToContainerOptions{
		AllowOverwriteDirWithFile: false,
	})
	if err != nil {
		return fmt.Errorf("copyToContainer: failed to copy: %s", err)
	}
	return nil
}

// RoundTripper is a round tripper that maps https://hs1 to the federation port of the container
// e.g https://localhost:35352
type RoundTripper struct {
	Deployment *Deployment
}

func (t *RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// map HS names to localhost:port combos
	hsName := req.URL.Hostname()
	dep, ok := t.Deployment.HS[hsName]
	if !ok {
		return nil, fmt.Errorf("dockerRoundTripper unknown hostname: '%s'", hsName)
	}
	newURL, err := url.Parse(dep.FedBaseURL)
	if err != nil {
		return nil, fmt.Errorf("dockerRoundTripper: failed to parase fedbaseurl for hs: %s", err)
	}
	req.URL.Host = newURL.Host
	req.URL.Scheme = "https"
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			ServerName:         hsName,
			InsecureSkipVerify: true,
		},
	}
	return transport.RoundTrip(req)
}
