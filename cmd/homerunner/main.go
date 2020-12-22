package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
)

type Config struct {
	HomeserverLifetimeMins int
	Port                   int
	VersionCheckIterations int
}

func NewConfig() *Config {
	cfg := &Config{
		HomeserverLifetimeMins: 30,
		Port:                   54321,
		VersionCheckIterations: 100,
	}
	if val, _ := strconv.Atoi(os.Getenv("HOMERUNNER_HS_LIFETIME_MINS")); val != 0 {
		cfg.HomeserverLifetimeMins = val
	}
	if val, _ := strconv.Atoi(os.Getenv("HOMERUNNER_PORT")); val != 0 {
		cfg.Port = val
	}
	if val, _ := strconv.Atoi(os.Getenv("HOMERUNNER_VER_CHECK_ITERATIONS")); val != 0 {
		cfg.VersionCheckIterations = val
	}
	return cfg
}

func main() {
	cfg := NewConfig()
	rt, err := NewRuntime(cfg)
	if err != nil {
		logrus.Fatalf("failed to setup new runtime: %s", err)
	}

	srv := &http.Server{
		ReadTimeout:  1 * time.Minute,
		WriteTimeout: 1 * time.Minute,
		Handler:      Routes(rt, cfg),
		Addr:         fmt.Sprintf("0.0.0.0:%d", cfg.Port),
	}
	logrus.Infof("Homerunner listening on :%d with config %+v", cfg.Port, cfg)

	if err := srv.ListenAndServe(); err != nil {
		logrus.Fatalf("ListenAndServe failed: %s", err)
	}
}
