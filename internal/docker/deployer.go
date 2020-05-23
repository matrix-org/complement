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
	"log"

	"github.com/docker/docker/api/types"
	client "github.com/docker/docker/client"
)

type Deployer struct {
	Namespace string
	Docker    *client.Client
	Counter   int
}

type Deployment struct {
	Deployer      *Deployer
	BlueprintName string
	HS            map[string]HomeserverDeployment
}

func (d *Deployment) Destroy() {
	d.Deployer.Destroy(d)
}

type HomeserverDeployment struct {
	BaseURL     string
	ContainerID string
}

func NewDeployer(namespace string) (*Deployer, error) {
	cli, err := client.NewEnvClient()
	if err != nil {
		return nil, err
	}
	return &Deployer{
		Namespace: namespace,
		Docker:    cli,
	}, nil
}

func (d *Deployer) Deploy(ctx context.Context, blueprintName string) (*Deployment, error) {
	dep := &Deployment{
		Deployer:      d,
		BlueprintName: blueprintName,
		HS:            make(map[string]HomeserverDeployment),
	}
	images, err := d.Docker.ImageList(ctx, types.ImageListOptions{
		Filters: label("complement_blueprint=" + blueprintName),
	})
	if err != nil {
		return nil, fmt.Errorf("Deploy: failed to ImageList: %w", err)
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("Deploy: No images have been built for blueprint %s", blueprintName)
	}
	for _, img := range images {
		d.Counter++
		contextStr := img.Labels["complement_context"]
		hsName := img.Labels["complement_hs_name"]
		// TODO: Make CSAPI port configurable
		hsURL, containerID, err := deployImage(
			d.Docker, img.ID, 8008, fmt.Sprintf("complement_%s_%s_%d", d.Namespace, contextStr, d.Counter),
			blueprintName, hsName, contextStr)
		if err != nil {
			return nil, fmt.Errorf("Deploy: Failed to deploy image %+v : %w", img, err)
		}
		log.Printf("%s -> %s (%s)\n", contextStr, hsURL, containerID)
		dep.HS[hsName] = HomeserverDeployment{
			BaseURL:     hsURL,
			ContainerID: containerID,
		}
	}
	return dep, nil
}

func (d *Deployer) Destroy(dep *Deployment) {
	for _, hsDep := range dep.HS {
		err := d.Docker.ContainerKill(context.Background(), hsDep.ContainerID, "KILL")
		if err != nil {
			log.Printf("Destroy: Failed to destroy container %s : %w\n", hsDep.ContainerID, err)
		}
	}
}
