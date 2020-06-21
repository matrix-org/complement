package config

import (
	"os"
	"strings"
)

type Complement struct {
	BaseImageURI        string
	BaseImageArgs       []string
	DebugLoggingEnabled bool
}

func NewConfigFromEnvVars() *Complement {
	cfg := &Complement{}
	cfg.BaseImageURI = os.Getenv("COMPLEMENT_BASE_IMAGE")
	cfg.BaseImageArgs = strings.Split(os.Getenv("COMPLEMENT_BASE_IMAGE_ARGS"), " ")
	cfg.DebugLoggingEnabled = os.Getenv("COMPLEMENT_DEBUG") == "1"
	return cfg
}
