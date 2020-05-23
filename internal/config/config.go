package config

import (
	"os"
	"strings"
)

type Complement struct {
	BaseImageURI  string
	BaseImageArgs []string
}

func NewConfigFromEnvVars() *Complement {
	cfg := &Complement{}
	cfg.BaseImageURI = os.Getenv("COMPLEMENT_BASE_IMAGE")
	cfg.BaseImageArgs = strings.Split(os.Getenv("COMPLEMENT_BASE_IMAGE_ARGS"), " ")
	return cfg
}
