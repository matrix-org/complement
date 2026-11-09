//go:build dendrite_blacklist
// +build dendrite_blacklist

package runtime

import (
	"context"

	"github.com/moby/moby/client"
)

func init() {
	Homeserver = Dendrite
	// For Dendrite, we want to always stop the container gracefully, as this is needed to
	// extract e.g. coverage reports.
	ContainerKillFunc = func(cli *client.Client, containerID string) error {
		oneSecond := 1
		_, err := cli.ContainerStop(context.Background(), containerID, client.ContainerStopOptions{
			Timeout: &oneSecond,
		})
		return err
	}
}
