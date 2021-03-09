package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/config"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/sirupsen/logrus"
)

type Runtime struct {
	Config                *Config
	mu                    *sync.Mutex
	BlueprintToDeployment map[string]*docker.Deployment
	BlueprintToTimer      map[string]*time.Timer
}

// NewRuntime makes a homerunner runtime
func NewRuntime(cfg *Config) (*Runtime, error) {
	return &Runtime{
		Config:                cfg,
		BlueprintToDeployment: make(map[string]*docker.Deployment),
		BlueprintToTimer:      make(map[string]*time.Timer),
		mu:                    &sync.Mutex{},
	}, nil
}

func (r *Runtime) CreateDeployment(imageURI string, blueprint *b.Blueprint) (*docker.Deployment, time.Time, error) {
	duration := time.Duration(r.Config.HomeserverLifetimeMins) * time.Minute
	var expires time.Time
	if blueprint == nil {
		return nil, expires, fmt.Errorf("blueprint must be supplied")
	}
	namespace := "homerunner_" + blueprint.Name
	cfg := &config.Complement{
		BaseImageURI:           imageURI,
		DebugLoggingEnabled:    true,
		VersionCheckIterations: r.Config.VersionCheckIterations,
		BestEffort:             true,
	}
	builder, err := docker.NewBuilder(cfg)
	if err != nil {
		return nil, expires, err
	}
	if err = builder.ConstructBlueprintsIfNotExist([]b.Blueprint{*blueprint}); err != nil {
		return nil, expires, fmt.Errorf("CreateDeployment: Failed to construct blueprint: %s", err)
	}
	d, err := docker.NewDeployer(namespace, cfg)
	if err != nil {
		return nil, expires, fmt.Errorf("CreateDeployment: NewDeployer returned error %s", err)
	}
	dep, err := d.Deploy(context.Background(), blueprint.Name)
	if err != nil {
		return nil, expires, fmt.Errorf("CreateDeployment: Deploy returned error %s", err)
	}
	if err := r.addDeployment(blueprint.Name, dep, duration); err != nil {
		return nil, expires, err
	}
	return dep, time.Now().Add(duration), nil
}

func (r *Runtime) addDeployment(blueprintName string, d *docker.Deployment, duration time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.BlueprintToDeployment[blueprintName]; ok {
		return fmt.Errorf("deployment with name %s already exists", blueprintName)
	}
	r.BlueprintToDeployment[blueprintName] = d
	r.BlueprintToTimer[blueprintName] = time.AfterFunc(duration, func() {
		logrus.Infof("Blueprint '%s' has expired. Tearing down network.", blueprintName)
		err := r.DestroyDeployment(blueprintName)
		if err != nil {
			logrus.WithError(err).Errorf("Failed to tear down expired blueprint '%s'", blueprintName)
		}
	})
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
	timer := r.BlueprintToTimer[blueprintName]
	timer.Stop()
	delete(r.BlueprintToTimer, blueprintName)
	return nil
}
