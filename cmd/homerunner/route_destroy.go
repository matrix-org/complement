package main

import (
	"context"
	"fmt"

	"github.com/matrix-org/util"
)

type ReqDestroy struct {
	BlueprintName string `json:"blueprint_name"`
}

type ResDestroy struct {
}

func RouteDestroy(ctx context.Context, rt *Runtime, rc *ReqDestroy) util.JSONResponse {
	if rc.BlueprintName == "" {
		return util.MessageResponse(400, "missing blueprint name")
	}
	err := rt.DestroyDeployment(rc.BlueprintName)
	if err != nil {
		return util.MessageResponse(500, fmt.Sprintf("failed to destroy deployment: %s", err))
	}
	return util.JSONResponse{
		Code: 200,
		JSON: ResDestroy{},
	}
}
