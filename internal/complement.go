package internal

import (
	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/config"
	"github.com/matrix-org/complement/internal/docker"
)

func Start(cfg *config.Complement) (*docker.Builder, error) {
	builder, err := docker.NewBuilder(cfg)
	if err != nil {
		return nil, err
	}
	builder.Cleanup()
	// Add additional static blueprints here
	return builder, builder.ConstructBlueprints([]b.Blueprint{
		b.BlueprintCleanHS,
		b.BlueprintOneToOneRoom,
		// b.BlueprintFederationOneToOneRoom,
	})
}
