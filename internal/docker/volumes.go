package docker

import (
	"context"

	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/volume"
	client "github.com/docker/docker/client"
)

// A volume is a mounted directory on the homeserver container
type Volume interface {
	// Return the mount point
	Mount() mount.Mount
	// Prepare the mount point. `contextStr` is unique to this blueprint+homeserver for homeserver
	// specific mounts e.g appservices.
	Prepare(ctx context.Context, docker *client.Client, contextStr string) error
}

type VolumeAppService struct {
	source string
}

func (v *VolumeAppService) Prepare(ctx context.Context, docker *client.Client, contextStr string) error {
	asVolume, err := docker.VolumeCreate(context.Background(), volume.VolumesCreateBody{
		Name: "appservices",
	})
	if err != nil {
		return err
	}
	v.source = asVolume.Name
	return nil
}

func (v *VolumeAppService) Mount() mount.Mount {
	return mount.Mount{
		Type:   mount.TypeVolume,
		Source: v.source,
		Target: "/appservices",
	}
}
