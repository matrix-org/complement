package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"os"

	"github.com/matrix-org/complement/config"
	"github.com/matrix-org/complement/internal/docker"
)

var (
	flagName   = flag.String("name", "", "The name to attach to this homeserver run. E.g 'dendrite 0.6.4'.")
	flagSeed   = flag.Int64("seed", 0, "The seed to use for deterministic tests. This allows homeservers to be compared.")
	flagImage  = flag.String("image", "", "Required. The complement-compatible homserver image to use.")
	flagOutput = flag.String("output", "output.json", "Where to write the output data")
)

type Output struct {
	Name      string
	Snapshots []Snapshot
	Seed      int64
	BaseImage string
}

type Config struct {
	BaseImage string
	Seed      int64
}

func main() {
	flag.Parse()
	cfg := Config{
		BaseImage: *flagImage,
		Seed:      *flagSeed,
	}
	// initialise complement
	complementConfig := config.NewConfigFromEnvVars("perf", cfg.BaseImage)
	complementConfig.DebugLoggingEnabled = true

	builder, err := docker.NewBuilder(complementConfig)
	if err != nil {
		panic(err)
	}
	builder.Cleanup() // remove any previous runs
	deployer, err := docker.NewDeployer("perf", complementConfig)
	if err != nil {
		panic(err)
	}

	// run the test
	snapshots, err := runTest("my_test", builder, deployer, cfg.Seed)
	if err != nil {
		panic(err)
	}
	b, err := json.Marshal(Output{
		Snapshots: snapshots,
		Seed:      *flagSeed,
		BaseImage: *flagImage,
		Name:      *flagName,
	})
	if err != nil {
		panic(err)
	}
	if err = ioutil.WriteFile(*flagOutput, b, os.ModePerm); err != nil {
		panic(err)
	}
}
