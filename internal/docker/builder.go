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
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	client "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/config"
	"github.com/matrix-org/complement/internal/instruction"
)

const complementLabel = "complement_context"

type Builder struct {
	BaseImage      string
	ImageArgs      []string
	CSAPIPort      int
	FederationPort int
	Docker         *client.Client
	debugLogging   bool
}

func NewBuilder(cfg *config.Complement) (*Builder, error) {
	cli, err := client.NewEnvClient()
	if err != nil {
		return nil, err
	}
	return &Builder{
		Docker:         cli,
		BaseImage:      cfg.BaseImageURI,
		ImageArgs:      cfg.BaseImageArgs,
		CSAPIPort:      8008,
		FederationPort: 8448,
		debugLogging:   cfg.DebugLoggingEnabled,
	}, nil
}

func (b *Builder) log(str string, args ...interface{}) {
	if !b.debugLogging {
		return
	}
	log.Printf(str, args...)
}

func (b *Builder) Cleanup() {
	err := b.removeContainers()
	if err != nil {
		b.log("Cleanup: Failed to remove containers: %s", err)
	}
	err = b.removeImages()
	if err != nil {
		b.log("Cleanup: Failed to remove images: %s", err)
	}
	err = b.removeNetworks()
	if err != nil {
		b.log("Cleanup: Failed to remove networks: %s", err)
	}
}

// removeImages removes all images with `complementLabel`.
func (b *Builder) removeNetworks() error {
	networks, err := b.Docker.NetworkList(context.Background(), types.NetworkListOptions{
		Filters: label(complementLabel),
	})
	if err != nil {
		return err
	}
	for _, nw := range networks {
		err = b.Docker.NetworkRemove(context.Background(), nw.ID)
		if err != nil {
			return err
		}
	}

	return nil
}

// removeImages removes all images with `complementLabel`.
func (b *Builder) removeImages() error {
	images, err := b.Docker.ImageList(context.Background(), types.ImageListOptions{
		Filters: label(complementLabel),
	})
	if err != nil {
		return err
	}
	for _, img := range images {
		_, err = b.Docker.ImageRemove(context.Background(), img.ID, types.ImageRemoveOptions{
			Force: true,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// removeContainers removes all containers with `complementLabel`.
func (b *Builder) removeContainers() error {
	containers, err := b.Docker.ContainerList(context.Background(), types.ContainerListOptions{
		All:     true,
		Filters: label(complementLabel),
	})
	if err != nil {
		return err
	}
	for _, c := range containers {
		err = b.Docker.ContainerRemove(context.Background(), c.ID, types.ContainerRemoveOptions{
			Force: true,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *Builder) ConstructBlueprints(bs []b.Blueprint) error {
	var bpWg sync.WaitGroup
	bpWg.Add(len(bs))
	for _, bprint := range bs {
		go b.construct(bprint, &bpWg)
	}
	bpWg.Wait()
	// wait a bit for images/containers to show up in 'image ls'
	foundImages := false
	for i := 0; i < 50; i++ { // max 5s
		images, err := b.Docker.ImageList(context.Background(), types.ImageListOptions{
			Filters: label(complementLabel),
		})
		if err != nil {
			return err
		}
		if len(images) < len(bs) {
			time.Sleep(100 * time.Millisecond)
		} else {
			foundImages = true
			break
		}
	}
	// do this after we have found images so we know that the containers have been detached so
	// we can actually remove the networks.
	b.removeNetworks()
	if !foundImages {
		return fmt.Errorf("failed to find built images via ImageList: did they all build ok?")
	}
	return nil
}

// construct all Homeservers sequentially then commits them
func (b *Builder) construct(bprint b.Blueprint, bpWg *sync.WaitGroup) {
	defer bpWg.Done()
	networkID := createNetwork(b.Docker, bprint.Name)
	if networkID == "" {
		return
	}

	runner := instruction.NewRunner(bprint.Name, b.debugLogging)
	results := make([]result, len(bprint.Homeservers))
	for i, hs := range bprint.Homeservers {
		res := b.constructHomeserver(bprint.Name, runner, hs, networkID)
		if res.err != nil && res.containerID != "" {
			// print docker logs because something went wrong
			printLogs(b.Docker, res.containerID, res.contextStr)
		}
		// kill the container
		defer func(r result) {
			killErr := b.Docker.ContainerKill(context.Background(), r.containerID, "KILL")
			if killErr != nil {
				b.log("%s : Failed to kill container %s: %s\n", r.contextStr, r.containerID, killErr)
			}
		}(res)
		results[i] = res
	}

	// commit containers
	for _, res := range results {
		if res.err != nil {
			continue
		}
		labels := labelsForTokens(runner.AccessTokens(res.homeserver.Name))

		// commit the container
		commit, err := b.Docker.ContainerCommit(context.Background(), res.containerID, types.ContainerCommitOptions{
			Author:    "Complement",
			Pause:     true,
			Reference: "localhost/complement:" + res.contextStr,
			Config: &container.Config{
				Labels: labels,
			},
		})
		if err != nil {
			b.log("%s : failed to ContainerCommit: %s\n", res.contextStr, err)
			return
		}
		imageID := strings.Replace(commit.ID, "sha256:", "", 1)
		b.log("%s => %s\n", res.contextStr, imageID)
	}
}

// construct this homeserver and execute its instructions, keeping the container alive.
func (b *Builder) constructHomeserver(blueprintName string, runner *instruction.Runner, hs b.Homeserver, networkID string) result {
	contextStr := fmt.Sprintf("%s.%s", blueprintName, hs.Name)
	b.log("%s : constructing homeserver...\n", contextStr)
	dep, err := b.deployBaseImage(blueprintName, hs.Name, contextStr, networkID)
	if err != nil {
		b.log("%s : failed to deployBaseImage: %s\n", contextStr, err)
		containerID := ""
		if dep != nil {
			containerID = dep.ContainerID
		}
		printLogs(b.Docker, containerID, contextStr)
		return result{
			err: err,
		}
	}
	b.log("%s : deployed base image to %s (%s)\n", contextStr, dep.BaseURL, dep.ContainerID)
	err = runner.Run(hs, dep.BaseURL)
	if err != nil {
		b.log("%s : failed to run instructions: %s\n", contextStr, err)
	}
	return result{
		err:         err,
		containerID: dep.ContainerID,
		contextStr:  contextStr,
		homeserver:  hs,
	}
}

// deployBaseImage runs the base image and returns the baseURL, containerID or an error.
func (b *Builder) deployBaseImage(blueprintName, hsName, contextStr, networkID string) (*HomeserverDeployment, error) {
	return deployImage(b.Docker, b.BaseImage, b.CSAPIPort, fmt.Sprintf("complement_%s", contextStr), blueprintName, hsName, contextStr, networkID)
}

func deployImage(docker *client.Client, imageID string, csPort int, containerName, blueprintName, hsName, contextStr, networkID string) (*HomeserverDeployment, error) {
	ctx := context.Background()
	body, err := docker.ContainerCreate(ctx, &container.Config{
		Image:      imageID,
		Domainname: hsName,
		Hostname:   hsName,
		Env:        []string{"SERVER_NAME=" + hsName},
		//Cmd:   b.ImageArgs,
		Labels: map[string]string{
			complementLabel:        contextStr,
			"complement_blueprint": blueprintName,
			"complement_hs_name":   hsName,
		},
	}, &container.HostConfig{
		PublishAllPorts: true,
	}, &network.NetworkingConfig{
		map[string]*network.EndpointSettings{
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
	err = docker.ContainerStart(ctx, containerID, types.ContainerStartOptions{})
	if err != nil {
		return nil, err
	}
	inspect, err := docker.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, err
	}
	baseURL, fedBaseURL, err := endpoints(inspect.NetworkSettings.Ports, 8008, 8448)
	if err != nil {
		return nil, fmt.Errorf("%s : image %s : %w", contextStr, imageID, err)
	}
	versionsURL := fmt.Sprintf("%s/_matrix/client/versions", baseURL)
	// hit /versions to check it is up
	var lastErr error
	for i := 0; i < 20; i++ {
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
		BaseURL:      baseURL,
		FedBaseURL:   fedBaseURL,
		ContainerID:  containerID,
		AccessTokens: tokensFromLabels(inspect.Config.Labels),
	}
	if lastErr != nil {
		return d, fmt.Errorf("%s: failed to check server is up. %w", contextStr, lastErr)
	}
	return d, nil
}

func createNetwork(docker *client.Client, blueprintName string) (networkID string) {
	// make a user-defined network so we get DNS based on the container name
	nw, err := docker.NetworkCreate(context.Background(), "complement_"+blueprintName, types.NetworkCreate{
		Labels: map[string]string{
			complementLabel:        blueprintName,
			"complement_blueprint": blueprintName,
		},
	})
	if err != nil {
		log.Printf("Failed to create docker network: %s\n", err)
		return ""
	}
	if nw.Warning != "" {
		log.Printf("WARNING: %s\n", nw.Warning)
	}
	return nw.ID
}

func printLogs(docker *client.Client, containerID, contextStr string) {
	reader, err := docker.ContainerLogs(context.Background(), containerID, types.ContainerLogsOptions{
		ShowStderr: true,
		ShowStdout: true,
		Follow:     false,
	})
	if err != nil {
		log.Printf("%s : Failed to extract container logs: %s\n", contextStr, err)
		return
	}
	log.Printf("============================================\n\n\n")
	log.Printf("%s : Server logs:\n", contextStr)
	io.Copy(log.Writer(), reader)
	log.Printf("============== %s : END LOGS ==============\n\n\n", contextStr)
}

func label(in string) filters.Args {
	f := filters.NewArgs()
	f.Add("label", in)
	return f
}

func tokensFromLabels(labels map[string]string) map[string]string {
	userIDToToken := make(map[string]string)
	for k, v := range labels {
		if strings.HasPrefix(k, "access_token_") {
			userIDToToken[strings.TrimPrefix(k, "access_token_")] = v
		}
	}
	return userIDToToken
}

func labelsForTokens(userIDToToken map[string]string) map[string]string {
	labels := make(map[string]string)
	// collect and store access tokens as labels 'access_token_$userid: $token'
	for k, v := range userIDToToken {
		labels["access_token_"+k] = v
	}
	return labels
}

func endpoints(p nat.PortMap, csPort, ssPort int) (baseURL, fedBaseURL string, err error) {
	csapiPort := fmt.Sprintf("%d/tcp", csPort)
	csapiPortInfo, ok := p[nat.Port(csapiPort)]
	if !ok {
		return "", "", fmt.Errorf("port %s not exposed - exposed ports: %v", csapiPort, p)
	}
	baseURL = fmt.Sprintf("http://localhost:%s", csapiPortInfo[0].HostPort)

	ssapiPort := fmt.Sprintf("%d/tcp", ssPort)
	ssapiPortInfo, ok := p[nat.Port(ssapiPort)]
	if !ok {
		return "", "", fmt.Errorf("port %s not exposed - exposed ports: %v", ssapiPort, p)
	}
	fedBaseURL = fmt.Sprintf("https://localhost:%s", ssapiPortInfo[0].HostPort)
	return
}

type result struct {
	err         error
	containerID string
	contextStr  string
	homeserver  b.Homeserver
}
