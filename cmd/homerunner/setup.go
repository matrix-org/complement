package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/matrix-org/complement/internal/docker"
)

type Runtime struct {
	Client         *client.Client
	Config         *Config
	NetworkID      string
	mu             *sync.Mutex
	HSToDeployment map[string]*docker.HomeserverDeployment
}

// NewRuntime makes a homerunner runtime
func NewRuntime(cfg *Config) (*Runtime, error) {
	cli, err := client.NewEnvClient()
	if err != nil {
		return nil, err
	}
	nwID, err := docker.CreateNetwork(cli, "homerunner")
	if err != nil {
		return nil, err
	}
	return &Runtime{
		Client:         cli,
		NetworkID:      nwID,
		Config:         cfg,
		HSToDeployment: make(map[string]*docker.HomeserverDeployment),
		mu:             &sync.Mutex{},
	}, nil
}

func (r *Runtime) AddDeployment(hsName string, d *docker.HomeserverDeployment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.HSToDeployment[hsName]; ok {
		return fmt.Errorf("deployment with name %s already exists", hsName)
	}
	r.HSToDeployment[hsName] = d
	return nil
}

func (r *Runtime) DestroyDeployment(hsName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.HSToDeployment[hsName]
	if !ok {
		return fmt.Errorf("no deployment with name '%s' exists", hsName)
	}
	delete(r.HSToDeployment, hsName)
	err := r.Client.ContainerKill(context.Background(), d.ContainerID, "KILL")
	if err != nil {
		return err
	}
	return r.Client.ContainerRemove(context.Background(), d.ContainerID, types.ContainerRemoveOptions{
		Force: true,
	})
}
