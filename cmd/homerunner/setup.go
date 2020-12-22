package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/config"
	"github.com/matrix-org/complement/internal/docker"
)

type Runtime struct {
	Config                *Config
	mu                    *sync.Mutex
	BlueprintToDeployment map[string]*docker.Deployment
}

// NewRuntime makes a homerunner runtime
func NewRuntime(cfg *Config) (*Runtime, error) {
	return &Runtime{
		Config:                cfg,
		BlueprintToDeployment: make(map[string]*docker.Deployment),
		mu:                    &sync.Mutex{},
	}, nil
}

func (r *Runtime) CreateDeployment(imageURI string, blueprint *b.Blueprint) (*docker.Deployment, error) {
	if blueprint == nil {
		return nil, fmt.Errorf("blueprint must be supplied")
	}
	namespace := "homerunner_" + blueprint.Name
	cfg := &config.Complement{
		BaseImageURI:           imageURI,
		DebugLoggingEnabled:    true,
		VersionCheckIterations: r.Config.VersionCheckIterations,
	}
	builder, err := docker.NewBuilder(cfg)
	if err != nil {
		return nil, err
	}
	if err = builder.ConstructBlueprintsIfNotExist([]b.Blueprint{*blueprint}); err != nil {
		return nil, fmt.Errorf("CreateDeployment: Failed to construct blueprint: %s", err)
	}
	d, err := docker.NewDeployer(namespace, cfg)
	if err != nil {
		return nil, fmt.Errorf("CreateDeployment: NewDeployer returned error %s", err)
	}
	dep, err := d.Deploy(context.Background(), blueprint.Name)
	if err != nil {
		return nil, fmt.Errorf("CreateDeployment: Deploy returned error %s", err)
	}
	if err := r.addDeployment(blueprint.Name, dep); err != nil {
		return nil, err
	}
	return dep, nil
}

func (r *Runtime) addDeployment(blueprintName string, d *docker.Deployment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.BlueprintToDeployment[blueprintName]; ok {
		return fmt.Errorf("deployment with name %s already exists", blueprintName)
	}
	r.BlueprintToDeployment[blueprintName] = d
	return nil
}

func (r *Runtime) DestroyDeployment(blueprintName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.BlueprintToDeployment[blueprintName]
	if !ok {
		return fmt.Errorf("no deployment with name '%s' exists", blueprintName)
	}
	d.Deployer.Destroy(d, false)
	delete(r.BlueprintToDeployment, blueprintName)
	return nil
}
