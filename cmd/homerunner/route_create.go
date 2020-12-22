package main

import (
	"context"
	"fmt"
	"time"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/util"
)

type ReqCreate struct {
	BaseImageURI  string       `json:"base_image_uri"`
	BlueprintName string       `json:"blueprint_name"`
	Blueprint     *b.Blueprint `json:"blueprint"`
}

type ResCreate struct {
	Homeservers map[string]docker.HomeserverDeployment `json:"homeservers"`
	Expires     time.Time                              `json:"expires"`
}

func RouteCreate(ctx context.Context, rt *Runtime, rc *ReqCreate) util.JSONResponse {
	if rc.BaseImageURI == "" {
		return util.MessageResponse(400, "missing base image uri")
	}
	if rc.BlueprintName == "" && rc.Blueprint == nil {
		return util.MessageResponse(400, "one of 'blueprint_name' or 'blueprint' must be specified")
	}
	knownBlueprints := map[string]*b.Blueprint{
		b.BlueprintCleanHS.Name:                &b.BlueprintCleanHS,
		b.BlueprintFederationOneToOneRoom.Name: &b.BlueprintFederationOneToOneRoom,
	}
	knownBlueprint, ok := knownBlueprints[rc.BlueprintName]
	if ok {
		rc.Blueprint = knownBlueprint
	}
	if rc.Blueprint == nil {
		return util.MessageResponse(400, "missing blueprint")
	}
	dep, err := rt.CreateDeployment(rc.BaseImageURI, rc.Blueprint)
	if err != nil {
		return util.MessageResponse(400, fmt.Sprintf("failed to create deployment: %s", err))
	}
	expires := time.Now().Add(time.Duration(rt.Config.HomeserverLifetimeMins) * time.Minute)
	return util.JSONResponse{
		Code: 200,
		JSON: ResCreate{
			Homeservers: dep.HS,
			Expires:     expires,
		},
	}
}
