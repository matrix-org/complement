package main

import (
	"context"
	"fmt"
	"time"

	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/util"
)

type ReqCreate struct {
	Image  string `json:"image"`
	HSName string `json:"hs_name"`
}

type ResCreate struct {
	HSURL   string    `json:"hs_url"`
	Expires time.Time `json:"expires"`
}

func RouteCreate(ctx context.Context, rt *Runtime, rc *ReqCreate) util.JSONResponse {
	if rc.Image == "" || rc.HSName == "" {
		return util.MessageResponse(400, "missing image/hs name")
	}
	dep, err := docker.DeployImage(rt.Client, rc.Image, 8008, rc.HSName, rc.HSName, rc.HSName, rc.HSName, rt.NetworkID, rt.Config.VersionCheckIterations)
	if err != nil {
		return util.MessageResponse(500, fmt.Sprintf("failed to deploy image: %s", err))
	}
	expires := time.Now().Add(time.Duration(rt.Config.HomeserverLifetimeMins) * time.Minute)
	err = rt.AddDeployment(rc.HSName, dep)
	if err != nil {
		return util.MessageResponse(400, fmt.Sprintf("failed to add deployment: %s", err))
	}
	return util.JSONResponse{
		Code: 200,
		JSON: ResCreate{
			HSURL:   dep.BaseURL,
			Expires: expires,
		},
	}
}
