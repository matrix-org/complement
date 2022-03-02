package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/matrix-org/complement/internal/docker"
)

type Snapshot struct {
	Name             string
	HSName           string
	Duration         time.Duration
	AbsoluteDuration time.Duration
	MemoryUsage      uint64
	CPUUserland      uint64
	CPUKernel        uint64
	BytesWritten     uint64
	BytesRead        uint64
	TxBytes          int64
	RxBytes          int64
}

func snapshotStats(spanName string, deployment *docker.Deployment, absDuration, duration time.Duration) (snapshots []Snapshot) {
	for hsName, hsInfo := range deployment.HS {
		stats, err := deployment.Deployer.Docker.ContainerStatsOneShot(context.Background(), hsInfo.ContainerID)
		if err != nil {
			return nil
		}
		var sj types.StatsJSON
		err = json.NewDecoder(stats.Body).Decode(&sj)
		stats.Body.Close()
		if err != nil {
			return nil
		}

		var rxBytes, txBytes int64
		for _, nw := range sj.Networks {
			rxBytes += int64(nw.RxBytes)
			txBytes += int64(nw.TxBytes)
		}
		var bw, br uint64
		for _, block := range sj.BlkioStats.IoServiceBytesRecursive {
			if block.Op == "read" {
				br = block.Value
			} else if block.Op == "write" {
				bw = block.Value
			}
		}
		snapshots = append(snapshots, Snapshot{
			HSName:           hsName,
			Name:             spanName,
			Duration:         duration,
			AbsoluteDuration: absDuration,
			MemoryUsage:      sj.MemoryStats.Usage,
			CPUUserland:      sj.CPUStats.CPUUsage.UsageInUsermode,
			CPUKernel:        sj.CPUStats.CPUUsage.UsageInKernelmode,
			TxBytes:          txBytes,
			RxBytes:          rxBytes,
			BytesWritten:     bw,
			BytesRead:        br,
		})
	}
	return
}
