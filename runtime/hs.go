package runtime

import (
	"context"

	"github.com/docker/docker/client"
	"github.com/matrix-org/complement/ct"
)

const (
	Dendrite  = "dendrite"
	Synapse   = "synapse"
	Conduit   = "conduit"
	Conduwuit = "conduwuit"
)

var Homeserver string

// ContainerKillFunc is used to destroy a container, it can be overwritten by Homeserver implementations
// to e.g. gracefully stop a container.
var ContainerKillFunc = func(client *client.Client, containerID string) error {
	return client.ContainerKill(context.Background(), containerID, "KILL")
}

// Skip the test (via t.Skipf) if the homeserver being tested matches one of the homeservers, else return.
//
// The homeserver being tested is detected via the presence of a `*_blacklist` tag e.g:
//
//	go test -tags="dendrite_blacklist"
//
// This means it is important to always specify this tag when running tests. Failure to do
// so will result in a warning being printed to stdout, and the test will be run. When a new server
// implementation is added, a respective `hs_$name.go` needs to be created in this directory. This
// file pairs together the tag name with a string constant declared in this package
// e.g. dendrite_blacklist == runtime.Dendrite
func SkipIf(t ct.TestLike, hses ...string) {
	t.Helper()
	for _, hs := range hses {
		if Homeserver == hs {
			t.Skipf("skipped on %s", hs)
			return
		}
	}
	if Homeserver == "" {
		// they ran Complement without a blacklist so it's impossible to know what HS they are
		// running, warn them.
		t.Logf(
			"WARNING: %s called runtime.SkipIf(%v) but Complement doesn't know which HS is running as it was run without a *_blacklist tag: executing test.",
			t.Name(), hses,
		)
	}
}
