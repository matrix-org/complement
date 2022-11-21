package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/matrix-org/complement/internal/config"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/sirupsen/logrus"
)

const Pkg = "homerunner"

type Config struct {
	HomeserverLifetimeMins int
	Port                   int
	SpawnHSTimeout         time.Duration
	KeepBlueprints         []string
	Snapshot               string
	HSPortBindingIP        string
}

func (c *Config) DeriveComplementConfig(baseImageURI string) *config.Complement {
	cfg := config.NewConfigFromEnvVars(Pkg, baseImageURI)
	cfg.BestEffort = true
	cfg.KeepBlueprints = c.KeepBlueprints
	cfg.SpawnHSTimeout = c.SpawnHSTimeout
	cfg.HSPortBindingIP = c.HSPortBindingIP
	return cfg
}

func Getenv(key string, default_value string) string {
	value, exists := os.LookupEnv(key)
        if (exists) {
		return value
	} else {
		return default_value
	}
}

func NewConfig() *Config {
	cfg := &Config{
		HomeserverLifetimeMins: 30,
		Port:                   54321,
		SpawnHSTimeout:         5 * time.Second,
		KeepBlueprints:         strings.Split(os.Getenv("HOMERUNNER_KEEP_BLUEPRINTS"), " "),
		Snapshot:               os.Getenv("HOMERUNNER_SNAPSHOT_BLUEPRINT"),
		HSPortBindingIP:        Getenv("HOMERUNNER_HS_PORTBINDING_IP", "127.0.0.1"),
	}
	if val, _ := strconv.Atoi(os.Getenv("HOMERUNNER_LIFETIME_MINS")); val != 0 {
		cfg.HomeserverLifetimeMins = val
	}
	if val, _ := strconv.Atoi(os.Getenv("HOMERUNNER_PORT")); val != 0 {
		cfg.Port = val
	}
	if val, _ := strconv.Atoi(os.Getenv("HOMERUNNER_SPAWN_HS_TIMEOUT_SECS")); val != 0 {
		cfg.SpawnHSTimeout = time.Duration(val) * time.Second
	}
	return cfg
}

func cleanup(c *Config) {
	cfg := c.DeriveComplementConfig("nothing")
	builder, err := docker.NewBuilder(cfg)
	if err != nil {
		logrus.WithError(err).Fatalf("failed to run cleanup")
	}
	builder.Cleanup()
}

func main() {
	cfg := NewConfig()
	rt, err := NewRuntime(cfg)
	if err != nil {
		logrus.Fatalf("failed to setup new runtime: %s", err)
	}
	cleanup(cfg)

	if cfg.Snapshot != "" {
		logrus.Infof("Running in single-shot snapshot mode for request file '%s'", cfg.Snapshot)
		// pretend the file is the request
		reqFile, err := os.Open(cfg.Snapshot)
		if err != nil {
			logrus.Fatalf("failed to read snapshot: %s", err)
		}
		var rc ReqCreate
		if err := json.NewDecoder(reqFile).Decode(&rc); err != nil {
			logrus.Fatalf("file is not JSON: %s", err)
		}
		dep, _, err := rt.CreateDeployment(rc.BaseImageURI, rc.Blueprint)
		if err != nil {
			logrus.Fatalf("failed to create deployment: %s", err)
		}
		logrus.Infof("Successful deployment. Created %d homeserver images visible in 'docker image ls'.", len(dep.HS))
		logrus.Infof("Clients: to run this blueprint in homerunner, use the blueprint name '%s'", rc.Blueprint.Name)
		logrus.Infof("Servers: Run Homerunner with the env var HOMERUNNER_KEEP_BLUEPRINTS=%s set to prevent this blueprint being cleaned up", rc.Blueprint.Name)

		// clean up after ourselves
		_ = rt.DestroyDeployment(dep.BlueprintName)
		return
	}

	srv := &http.Server{
		ReadTimeout:  10 * time.Minute,
		WriteTimeout: 10 * time.Minute,
		Handler:      Routes(rt, cfg),
		Addr:         fmt.Sprintf("0.0.0.0:%d", cfg.Port),
	}
	logrus.Infof("Homerunner listening on :%d with config %+v", cfg.Port, cfg)

	if err := srv.ListenAndServe(); err != nil {
		logrus.Fatalf("ListenAndServe failed: %s", err)
	}
}
