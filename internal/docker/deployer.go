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
	networkID string
}

type Deployment struct {
	Deployer      *Deployer
	BlueprintName string
	HS            map[string]HomeserverDeployment
}

func (d *Deployment) Destroy(printServerLogs bool) {
	d.Deployer.Destroy(d, printServerLogs)
}

type HomeserverDeployment struct {
	BaseURL      string
	FedBaseURL   string
	ContainerID  string
	AccessTokens map[string]string
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
	networkID := createNetwork(d.Docker, blueprintName)
	if networkID == "" {
		return nil, fmt.Errorf("Deploy: failed to create network")
	}
	d.networkID = networkID
	for _, img := range images {
		d.Counter++
		contextStr := img.Labels["complement_context"]
		hsName := img.Labels["complement_hs_name"]
		// TODO: Make CSAPI port configurable
		deployment, err := deployImage(
			d.Docker, img.ID, 8008, fmt.Sprintf("complement_%s_%s_%d", d.Namespace, contextStr, d.Counter),
			blueprintName, hsName, contextStr, networkID)
		if err != nil {
			if deployment.ContainerID != "" {
				// print logs to help debug
				printLogs(d.Docker, deployment.ContainerID, contextStr)
			}
			return nil, fmt.Errorf("Deploy: Failed to deploy image %+v : %w", img, err)
		}
		log.Printf("%s -> %s (%s)\n", contextStr, deployment.BaseURL, deployment.ContainerID)
		dep.HS[hsName] = *deployment
	}
	return dep, nil
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
	}
	if d.networkID != "" {
		err := d.Docker.NetworkRemove(context.Background(), d.networkID)
		if err != nil {
			log.Printf("Destroy: Failed to destroy network %s : %s\n", d.networkID, err)
		}
	}
}
