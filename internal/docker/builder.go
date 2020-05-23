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
	}, nil
}

func (b *Builder) Cleanup() error {
	err := b.removeContainers()
	if err != nil {
		return err
	}
	return b.removeImages()
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
	if !foundImages {
		return fmt.Errorf("failed to find built images via ImageList: did they all build ok?")
	}
	return nil
}

func (b *Builder) construct(bprint b.Blueprint, bpWg *sync.WaitGroup) {
	defer bpWg.Done()
	var hsWg sync.WaitGroup
	hsWg.Add(len(bprint.Homeservers))
	for _, hs := range bprint.Homeservers {
		go b.constructHomeserver(bprint.Name, hs, &hsWg)
	}
	hsWg.Wait()
}

func (b *Builder) constructHomeserver(blueprintName string, hs b.Homeserver, hsWg *sync.WaitGroup) (string, error) {
	defer hsWg.Done()
	contextStr := fmt.Sprintf("%s.%s", blueprintName, hs.Name)
	log.Printf("%s : constructing homeserver...\n", contextStr)
	baseURL, containerID, err := b.deployBaseImage(blueprintName, hs.Name, contextStr)
	if err != nil {
		log.Printf("%s : failed to deployBaseImage: %s\n", contextStr, err)
		return "", err
	}
	log.Printf("%s : deployed base image to %s (%s)\n", contextStr, baseURL, containerID)
	runner := instruction.NewRunner(hs, contextStr)
	err = runner.Run(baseURL)
	defer func() {
		if err != nil {
			// print docker logs because something went wrong
			reader, err2 := b.Docker.ContainerLogs(context.Background(), containerID, types.ContainerLogsOptions{
				ShowStderr: true,
				ShowStdout: true,
				Follow:     false,
			})
			if err2 != nil {
				log.Printf("%s : Failed to extract container logs: %s\n", contextStr, err2)
			} else {
				io.Copy(log.Writer(), reader)
			}
		}
		// kill the container
		killErr := b.Docker.ContainerKill(context.Background(), containerID, "KILL")
		if killErr != nil {
			log.Printf("%s : Failed to kill container %s: %s\n", contextStr, containerID, killErr)
		}
	}()
	if err != nil {
		log.Printf("%s : failed to run instructions: %s\n", contextStr, err)
		return "", err
	}
	// commit the container
	res, err := b.Docker.ContainerCommit(context.Background(), containerID, types.ContainerCommitOptions{
		Author:    "Complement",
		Pause:     true,
		Reference: "localhost/complement:" + contextStr,
	})
	if err != nil {
		log.Printf("%s : failed to ContainerCommit: %s\n", contextStr, err)
		return "", err
	}
	imageID := strings.Replace(res.ID, "sha256:", "", 1)
	log.Printf("%s => %s\n", contextStr, imageID)
	return imageID, nil
}

// deployBaseImage runs the base image and returns the baseURL, containerID or an error.
func (b *Builder) deployBaseImage(blueprintName, hsName, contextStr string) (string, string, error) {
	return deployImage(b.Docker, b.BaseImage, b.CSAPIPort, fmt.Sprintf("complement_%s", contextStr), blueprintName, hsName, contextStr)
}

func deployImage(docker *client.Client, imageID string, csPort int, containerName, blueprintName, hsName, contextStr string) (string, string, error) {
	ctx := context.Background()
	body, err := docker.ContainerCreate(ctx, &container.Config{
		Image: imageID,
		//Cmd:   b.ImageArgs,
		Labels: map[string]string{
			complementLabel:        contextStr,
			"complement_blueprint": blueprintName,
			"complement_hs_name":   hsName,
		},
	}, &container.HostConfig{
		PublishAllPorts: true,
	}, nil, containerName)
	if err != nil {
		return "", "", err
	}
	containerID := body.ID
	err = docker.ContainerStart(ctx, containerID, types.ContainerStartOptions{})
	if err != nil {
		return "", "", err
	}
	inspect, err := docker.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", "", err
	}
	p := inspect.NetworkSettings.Ports
	csapiPort := fmt.Sprintf("%d/tcp", csPort)
	csapiPortInfo, ok := p[nat.Port(csapiPort)]
	if !ok {
		return "", "", fmt.Errorf("%s: image %s does not expose port %s - exposed: %v", contextStr, imageID, csapiPort, p)
	}
	baseURL := fmt.Sprintf("http://localhost:%s", csapiPortInfo[0].HostPort)
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
	if lastErr != nil {
		return "", "", fmt.Errorf("%s: failed to check server is up. %w", contextStr, lastErr)
	}
	return baseURL, containerID, nil
}

func label(in string) filters.Args {
	f := filters.NewArgs()
	f.Add("label", in)
	return f
}
