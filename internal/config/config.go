package config

import (
	"os"
	"strconv"
	"strings"
)

type Complement struct {
	BaseImageURI           string
	BaseImageArgs          []string
	DebugLoggingEnabled    bool
	AlwaysPrintServerLogs  bool
	BestEffort             bool
	VersionCheckIterations int
	KeepBlueprints         []string
	// The namespace for all complement created blueprints and deployments
	PackageNamespace string
}

func NewConfigFromEnvVars() *Complement {
	cfg := &Complement{}
	cfg.BaseImageURI = os.Getenv("COMPLEMENT_BASE_IMAGE")
	cfg.BaseImageArgs = strings.Split(os.Getenv("COMPLEMENT_BASE_IMAGE_ARGS"), " ")
	cfg.DebugLoggingEnabled = os.Getenv("COMPLEMENT_DEBUG") == "1"
	cfg.AlwaysPrintServerLogs = os.Getenv("COMPLEMENT_ALWAYS_PRINT_SERVER_LOGS") == "1"
	cfg.VersionCheckIterations = parseEnvWithDefault("COMPLEMENT_VERSION_CHECK_ITERATIONS", 100)
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
