package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"os"
	"time"

	"github.com/matrix-org/complement/internal/config"
	"github.com/matrix-org/complement/internal/docker"
)

var (
	flagName    = flag.String("name", "", "The name to attach to this homeserver run. E.g 'dendrite 0.6.4'.")
	flagSeed    = flag.Int64("seed", 0, "The seed to use for deterministic tests. This allows homeservers to be compared.")
	flagTimeout = flag.Int("timeout", 30, "The max time to wait in seconds for a homeserver to start.")
	flagImage   = flag.String("image", "", "Required. The complement-compatible homserver image to use.")
	flagOutput  = flag.String("output", "output.json", "Where to write the output data")
)

type Output struct {
	Name      string
	Snapshots []Snapshot
	Seed      int64
	BaseImage string
}

type Config struct {
	BaseImage      string
	Seed           int64
	SpawnHSTimeout time.Duration
}

func main() {
	flag.Parse()
	cfg := Config{
		BaseImage:      *flagImage,
		Seed:           *flagSeed,
		SpawnHSTimeout: time.Duration(*flagTimeout) * time.Second,
	}
	// initialise complement
	complementConfig := &config.Complement{
		BaseImageURI:        cfg.BaseImage,
		DebugLoggingEnabled: true,
		SpawnHSTimeout:      cfg.SpawnHSTimeout,
		PackageNamespace:    "perf",
	}
	if err := complementConfig.GenerateCA(); err != nil {
		panic(err)
	}
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
