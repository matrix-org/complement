package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/docker/docker/pkg/stdcopy"
)

type Snapshot struct {
	Name             string
	Description      string
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
	Smem             string
}

func snapshotStats(spanName, desc string, deployment *docker.Deployment, absDuration, duration time.Duration) (snapshots []Snapshot) {
	for hsName, hsInfo := range deployment.HS {
		dockerClient := deployment.Deployer.Docker

		// Capture the memory usage of the SBG and Sidecar processes.
		// Create an exec process in the container.
		exec, err := dockerClient.ContainerExecCreate(context.Background(), hsInfo.ContainerID, types.ExecConfig{
			Cmd: []string{
				"/usr/bin/smem",
				// Print the process "name" and "Proportional Set Size" columns only.
				"-c",
				"name pss",
				// Only show stats from certain processes.
				"-P",
				"(sbg|sidecar)",
				// Disable printing of the column header.
				"-H",
			},
			AttachStdout: true,
			AttachStderr: true,
		})
		if err != nil {
			panic(err)
		}

		// Attach to the exec process so that we can capture the output.
		resp, err := dockerClient.ContainerExecAttach(context.Background(), exec.ID, types.ExecStartCheck{})
		if err != nil {
			panic(err)
		}
		defer resp.Close()

		// Start the exec process (run smem).
		err = dockerClient.ContainerExecStart(context.Background(), exec.ID, types.ExecStartCheck{})
		if err != nil {
			panic(err)
		}

		// Wait for smem to finish
		for i := 0; i < 10; i++ {
			execResp, err := dockerClient.ContainerExecInspect(context.Background(), exec.ID)
			if err != nil {
				panic(err)
			}

			if !execResp.Running {
				break
			}

			time.Sleep(time.Millisecond * 500)
		}

		// Read the output from smem.
		smemOutputStdout := new(bytes.Buffer)
		_, err = stdcopy.StdCopy(smemOutputStdout, ioutil.Discard, resp.Reader)
		if err != nil {
			panic(err)
		}

		// Format the output of smem.
		formattedSmemOutput := formatSmemOutput(smemOutputStdout.String())

		stats, err := dockerClient.ContainerStatsOneShot(context.Background(), hsInfo.ContainerID)
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
			Description:      desc,
			Duration:         duration,
			AbsoluteDuration: absDuration,
			MemoryUsage:      sj.MemoryStats.Usage,
			CPUUserland:      sj.CPUStats.CPUUsage.UsageInUsermode,
			CPUKernel:        sj.CPUStats.CPUUsage.UsageInKernelmode,
			TxBytes:          txBytes,
			RxBytes:          rxBytes,
			BytesWritten:     bw,
			BytesRead:        br,
			Smem:             formattedSmemOutput,
		})
	}
	return
}

// formatSmemOutput formats the output of the smem command into
// something that's easier to read on a single line. It also filters out
// reporting the memory usage of the `smem` process itself.
// `smem` outputs memory usage in *bytes*.
//
// Given the input: "smem   11472 \nproc1   22827 \nproc2    26182 \n"
// It will convert it to: "proc1=22827,proc2=26182"
func formatSmemOutput(smemOutput string) string {
    // Split the string by newline
    lines := strings.Split(smemOutput, "\n")

    var result []string
    for _, line := range lines {
        // Use regex to find lines that match the desired format
        re := regexp.MustCompile(`^(\S+)\s+(\d+)\s*$`)
        matches := re.FindStringSubmatch(line)

		// Omit the memory of the `smem` command, which we don't care about.
        if len(matches) == 3 && matches[1] != "smem" {
            // Append the formatted string to the result slice
            result = append(result, fmt.Sprintf("%s=%s", matches[1], matches[2]))
        }
    }

    // Join the results with commas
    return strings.Join(result, ",")
}
