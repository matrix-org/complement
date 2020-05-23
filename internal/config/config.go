package config

import (
	"os"
	"strings"
)

type Complement struct {
	BaseImageURI  string
	BaseImageArgs []string
	DockerSock    string
	DockerPort    int
}

func NewConfigFromEnvVars() *Complement {
	cfg := &Complement{}
	cfg.BaseImageURI = os.Getenv("COMPLEMENT_BASE_IMAGE")
	cfg.BaseImageArgs = strings.Split(os.Getenv("COMPLEMENT_BASE_IMAGE_ARGS"), " ")
	cfg.DockerSock = os.Getenv("COMPLEMENT_DOCKER_SOCK")
	//cfg.DockerPort = os.Getenv("COMPLEMENT_DOCKER_PORT")
	return cfg
}
