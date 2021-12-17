package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Complement struct {
	BaseImageURI          string
	BaseImageArgs         []string
	DebugLoggingEnabled   bool
	AlwaysPrintServerLogs bool
	BestEffort            bool
	SpawnHSTimeout        time.Duration
	KeepBlueprints        []string
	// The namespace for all complement created blueprints and deployments
	PackageNamespace string
}

func NewConfigFromEnvVars() *Complement {
	cfg := &Complement{}
	cfg.BaseImageURI = os.Getenv("COMPLEMENT_BASE_IMAGE")
	cfg.BaseImageArgs = strings.Split(os.Getenv("COMPLEMENT_BASE_IMAGE_ARGS"), " ")
	cfg.DebugLoggingEnabled = os.Getenv("COMPLEMENT_DEBUG") == "1"
	cfg.AlwaysPrintServerLogs = os.Getenv("COMPLEMENT_ALWAYS_PRINT_SERVER_LOGS") == "1"
	cfg.SpawnHSTimeout = time.Duration(parseEnvWithDefault("COMPLEMENT_SPAWN_HS_TIMEOUT_SECS", 30)) * time.Second
	if os.Getenv("COMPLEMENT_VERSION_CHECK_ITERATIONS") != "" {
		fmt.Fprintln(os.Stderr, "Deprecated: COMPLEMENT_VERSION_CHECK_ITERATIONS will be removed in a later version. Use COMPLEMENT_SPAWN_HS_TIMEOUT_SECS instead which does the same thing and is clearer.")
		// each iteration had a 50ms sleep between tries so the timeout is 50 * iteration ms
		cfg.SpawnHSTimeout = time.Duration(50*parseEnvWithDefault("COMPLEMENT_VERSION_CHECK_ITERATIONS", 100)) * time.Millisecond
	}
	cfg.KeepBlueprints = strings.Split(os.Getenv("COMPLEMENT_KEEP_BLUEPRINTS"), " ")
	if cfg.BaseImageURI == "" {
		panic("COMPLEMENT_BASE_IMAGE must be set")
	}
	cfg.PackageNamespace = "pkg"
	return cfg
}

func parseEnvWithDefault(key string, def int) int {
	s := os.Getenv(key)
	if s != "" {
		i, err := strconv.Atoi(s)
		if err != nil {
			// Don't bother trying to report it
			return def
		}
		return i
	}
	return def
}
