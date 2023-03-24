//go:build dendrite_blacklist
// +build dendrite_blacklist

package runtime

import (
	"context"
	"time"

	"github.com/docker/docker/client"
)

func init() {
	Homeserver = Dendrite
	// For Dendrite, we want to always stop the container gracefully, as this is needed to
	// extract e.g. coverage reports.
	ContainerKillFunc = func(client *client.Client, containerID string) error {
		timeout := 1 * time.Second
		return client.ContainerStop(context.Background(), containerID, &timeout)
	}
}
