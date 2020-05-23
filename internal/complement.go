package internal

import (
	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/config"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/complement/internal/tests"
)

func Start(cfg *config.Complement) error {
	builder, err := docker.NewBuilder(cfg)
	if err != nil {
		return err
	}
	builder.Cleanup()
	defer builder.Cleanup()
	err = builder.ConstructBlueprints([]b.Blueprint{
		b.BlueprintCleanHS,
		b.BlueprintOneToOneRoom,
	})
	if err != nil {
		return err
	}
	return tests.Run()
}
