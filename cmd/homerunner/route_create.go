package main

import (
	"context"
	"fmt"
	"time"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/util"
)

type ReqCreate struct {
	BaseImageURI  string       `json:"base_image_uri"`
	BlueprintName string       `json:"blueprint_name"`
	Blueprint     *b.Blueprint `json:"blueprint"`
}

type ResCreate struct {
	Homeservers map[string]*docker.HomeserverDeployment `json:"homeservers"`
	Expires     time.Time                               `json:"expires"`
}

// RouteCreate handles creating blueprint deployments. There are 3 supported types of requests:
//   - A: Creating a blueprint from the static ones in `internal/b` : This is what Complement does.
//   - B: Creating an in-line blueprint where the blueprint is in the request.
//   - C: Creating a deployment from a pre-made blueprint image, e.g using account-snapshot.
func RouteCreate(ctx context.Context, rt *Runtime, rc *ReqCreate) util.JSONResponse {
	// Use case A: if the blueprint name is given, check for static ones
	knownBlueprint, ok := b.KnownBlueprints[rc.BlueprintName]
	if ok {
		// clobber it and pretend it's inline
		rc.Blueprint = knownBlueprint
	}

	if rc.Blueprint != nil {
		// Use cases A,B - we're making blueprints from scratch, meaning we need a base image
		if rc.BaseImageURI == "" {
			return util.MessageResponse(400, "missing base image uri")
		}
	} else if rc.BlueprintName != "" {
		// Use case C: the blueprint name isn't static, try it with just a name which will succeed
		// if the blueprint is already in the docker image cache.
		rc.Blueprint = &b.Blueprint{
			Name: rc.BlueprintName,
		}
		rc.BaseImageURI = "none"
	} else {
		return util.MessageResponse(400, "one of 'blueprint_name' or 'blueprint' must be specified")
	}

	dep, expires, err := rt.CreateDeployment(rc.BaseImageURI, rc.Blueprint)
	if err != nil {
		return util.MessageResponse(400, fmt.Sprintf("failed to create deployment: %s", err))
	}
	return util.JSONResponse{
		Code: 200,
		JSON: ResCreate{
			Homeservers: dep.HS,
			Expires:     expires,
		},
	}
}
