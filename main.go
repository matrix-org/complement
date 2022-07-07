package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/docker/docker/client"
)

func run(name string, arg ...string) error {
	fmt.Println(name, arg)
	cmd := exec.Command(name, arg...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func main() {
	ctx := context.Background()

	log.Println("Copying project files to volume...")
	run("sh", "-c", "rm -r /root/complement/*")
	err := run("sh", "-c", "cp -r /root/project/* /root/complement")
	if err != nil {
		panic(err)
	}

	client, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(err)
	}
	defer client.Close()
	for {
		_, err := client.Ping(ctx)
		if err == nil {
			break
		}
		log.Println("Waiting for dind...")
		time.Sleep(3 * time.Second)
	}

	log.Println("Building complement...")
	err = run("docker", "build",
		"--force-rm",
		"--tag", "complement",
		"--file", "dockerfiles/dind.complement.Dockerfile",
		"/root/complement")
	if err != nil {
		panic(err)
	}

	log.Println("Running complement...")
	errs := make([]error, 0)
	for _, path := range []string{"./tests/messagehub"} {
		err = run("docker", "run",
			"--rm",
			"--env", fmt.Sprintf("DOCKER_HOST=%s", client.DaemonHost()),
			"--env-file", "/root/complement/complement/.env",
			"--volume", "/root/complement:/root/complement",
			"--network", "host",
			"complement",
			"go", "test", "-v", path)
		if err != nil {
			errs = append(errs, fmt.Errorf("error running tests %s: %w", path, err))
		}
	}
	if len(errs) > 0 {
		panic(fmt.Errorf("errors: %v", errs))
	}
}
