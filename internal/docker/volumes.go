package docker

import (
	"context"
	"os"
	"path"

	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/volume"
	client "github.com/docker/docker/client"
)

// test 2
// A volume is a mounted directory on the homeserver container
type Volume interface {
	// Return the mount point
	Mount() mount.Mount
	// Prepare the mount point. `contextStr` is unique to this blueprint+homeserver for homeserver
	// specific mounts e.g appservices.
	Prepare(ctx context.Context, docker *client.Client, contextStr string) error
}

type VolumeCA struct {
	source string
	typ    mount.Type
}

// Prepare the Certificate Authority volume. This is independent of the homeserver calling Prepare
// hence the contextual string is unused.
func (v *VolumeCA) Prepare(ctx context.Context, docker *client.Client, x string) error {
	// Our CA cert is placed in the current working dir.
	// We bind mount this directory to all homeserver containers.
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	caCertificateDirHost := path.Join(cwd, "ca")
	if _, err := os.Stat(caCertificateDirHost); os.IsNotExist(err) {
		err = os.Mkdir(caCertificateDirHost, 0770)
		if err != nil {
			return err
		}
	}
	v.source = path.Join(cwd, "ca")
	v.typ = mount.TypeBind
	return nil
}

func (v *VolumeCA) Mount() mount.Mount {
	return mount.Mount{
		Type:   v.typ,
		Source: v.source,
		Target: "/ca",
	}
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
