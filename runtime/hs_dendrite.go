//go:build dendrite_blacklist
// +build dendrite_blacklist

package runtime

import (
	"context"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

func init() {
	Homeserver = Dendrite
	// For Dendrite, we want to always stop the container gracefully, as this is needed to
	// extract e.g. coverage reports.
	ContainerKillFunc = func(client *client.Client, containerID string) error {
		oneSecond := 1
		return client.ContainerStop(context.Background(), containerID, container.StopOptions{
			Timeout: &oneSecond,
		})
	}
}
