## How to run tests out-of-repo

- Make a new go project: `go mod init some.package.name`.
- Get the latest version of Complement: `go get github.com/matrix-org/complement@main`
- Add a tests directory and file: `mkdir ./tests; touch ./tests/something_test.go`

*something_test.go*
```go
package tests

import (
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

func TestCannotKickNonPresentUser(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	resp := alice.Do(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "kick"},
		client.WithJSONBody(t, map[string]interface{}{
			"user_id": bob.UserID,
			"reason":  "testing",
		}),
	)

	must.MatchResponse(t, resp, match.HTTPResponse{
		StatusCode: 403,
	})
}
```

Complement needs to be bootstrapped in when running `go test`. This is doing via a `./tests/main_test.go` file:
```go
package tests

import (
	"testing"

	"github.com/matrix-org/complement"
)

func TestMain(m *testing.M) {
	complement.TestMain(m, "some_namespace_for_these_tests")
}
```
If you are only running these tests and no other Complement tests (e.g the main ones in the Complement repo) *in parallel* (i.e `go test ./complement/tests ./myrepo/tests`) then the namespace value doesn't matter. If you _are_ running other tests in parallel the namespace needs to be unique for all possible Complement test packages, otherwise you will get container conflicts. So don't call it "fed" or "csapi" as they are used by the main Complement tests.

Now set up a `COMPLEMENT_BASE_IMAGE` and run `COMPLEMENT_BASE_IMAGE=homeserver:latest go test -v ./tests`:
```
2023/10/25 14:39:51 config: &{BaseImageURI:homeserver:latest DebugLoggingEnabled:false AlwaysPrintServerLogs:false EnvVarsPropagatePrefix: SpawnHSTimeout:30s KeepBlueprints:[] HostMounts:[] BaseImageURIs:map[] PackageNamespace:oor CACertificate:0x14000cea580 CAPrivateKey:0x14000cf5300 BestEffort:false HostnameRunningComplement:host.docker.internal EnableDirtyRuns:false HSPortBindingIP:127.0.0.1 PostTestScript:}
=== RUN   TestCannotKickNonPresentUser
    foo_test.go:14: Deploy times: 4.543523667s blueprints, 3.352760416s containers
    client.go:621: [CSAPI] POST hs1/_matrix/client/v3/register => 200 OK (16.544958ms)
    client.go:621: [CSAPI] POST hs1/_matrix/client/v3/register => 200 OK (13.813708ms)
    client.go:621: [CSAPI] POST hs1/_matrix/client/v3/createRoom => 200 OK (56.164792ms)
    client.go:621: [CSAPI] POST hs1/_matrix/client/v3/rooms/!ajUaasESMwLZSTpzkq:hs1/kick => 403 Forbidden (19.165125ms)
--- PASS: TestCannotKickNonPresentUser (8.31s)
PASS
ok  	oor/tests	8.829s
```


NOTE: You currently cannot set up mock federation servers as that package is still internal. You can test CSAPI and deploy >1 HS though.