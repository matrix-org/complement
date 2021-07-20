# So you want to write Complement tests

Complement is a black box integration testing framework for Matrix homeservers.

This document will outline how Complement works and how you can add efficient tests and best practices for Complement itself.  If you haven't run Complement tests yet, please see the [README](README.md) and start there!

## Terminology

* `Blueprint`: a human-readable outline for what should be done prior to a test (such as creating users, rooms, etc).
* `Deployment`: controls the lifetime of a Docker container (built from a `Blueprint`). It has functions on it for creating deployment-scoped structs such as Client-Server API clients for interacting with specific homeservers in the deployment.

## Architecture

Each Complement test runs one or more Docker containers for the homeserver(s) involved in the test. These containers are snapshots of the target homeserver at a particular state. The state of each container is determined by the `Blueprint` used. Client-Server and Server-Server API calls can then be made with assertions against the results.

In order to actually write a test, Complement needs to:

* Create `Deployments`, this is done by calling `deployment := Deploy(...)` (and can subsequently be killed via `deployment.Destroy(...)`).
* (Potentially) make API calls against the `Deployments`.
* Make assertions, this can be done via the standard Go testing mechanisms (e.g. `t.Fatalf`), but Complement also provides some helpers in the `must` and `match` packages.

For testing outbound federation, Complement implements a bare-bones Federation server for homeservers to talk to.  Each test must explicitly declare the functionality of the homeserver. This is done using [functional options](https://dave.cheney.net/2014/10/17/functional-options-for-friendly-apis) and looks something like:

```go
// A federation server which handles serving up its own keys when requested,
// automatically accepts make_join and send_join requests and deals with
// room alias lookups.
srv := federation.NewServer(t, deployment,
    federation.HandleKeyRequests(),
    federation.HandleMakeSendJoinRequests(),
    federation.HandleDirectoryLookups(),
)
// begin listening on a goroutine
cancel := srv.Listen()

// Shutdown the server at test end
defer cancel()
```

Network topology for all forms of communication look like this:
```
+------+                  Outbound federation           +-----------+             Network: one per blueprint                +-----------+
|      | :12345 <------ host.docker.internal:12345 ---- |           |                                                       |           |
| Host |                                                | Container | ------- SS API https://hs2/_matrix/federation/... --> | Container |
|      | -------CS API http://localhost:54321 --------> |   (hs1)   |                                                       |   (hs2)   |
|      | ------SS API https://localhost:55555 --------> |           |                                                       |           |
+------+                                                +-----------+                                                       +-----------+
                                             docker -p 54321:8008 -p 55555:8448

The high numbered ports are randomly chosen, and are for illustrative purposes only.
```
The mapping of `hs1` to `localhost:port` combinations can be done automatically using a `docker.RoundTripper`.

## How do I...

Get a Client-Server API client:
```go
// the user and homeserver name are from the blueprint
// automatically maps localhost:12345 to the right container
alice := deployment.Client(t, "hs1", "@alice:hs1")
```

Make a Federation server:
```go
srv := federation.NewServer(t, deployment,
    federation.HandleKeyRequests(),
    federation.HandleMakeSendJoinRequests(),
)
cancel := srv.Listen()
defer cancel()
```

Get a Federation client:
```go
// Federation servers sign their requests, so you need a server before
// you can make a client.
// automatically accepts self-signed TLS certs
// automatically maps localhost:12345 to the right container
// automatically signs requests
// srv == federation.Server
fedClient := srv.FederationClient(deployment)
```

## FAQ

### How should I name the test files / test functions?

Test files have to have `_test.go` else Go won't run the tests in that file. Other than that, there are no restrictions or naming convention.
If you are converting a sytest be sure to add a comment _anywhere_ in the source code which has the form:
```go
// sytest: $test_name
```
e.g:
```go
// Test that a server can receive /keys requests:
// https://matrix.org/docs/spec/server_server/latest#get-matrix-key-v2-server-keyid
// sytest: Federation key API allows unsigned requests for keys
func TestInboundFederationKeys(t *testing.T) {
    ...
}
```
Adding `// sytest: ...` means `sytest_coverage.go` will know the test is converted and automatically update the list
when run! Use `go run sytest_coverage.go -v` to see the exact string to use, as they may be different to the one produced
by an actual sytest run due to parameterised tests.

### Should I always make a new blueprint for a test?

Probably not. Blueprints are costly, and they should only be made if there is a strong case for plenty of reuse among tests. In the same way that we don't always add fixtures to sytest, we should be sparing with adding blueprints.

### How should I assert JSON objects?

Use one of the matchers in the `match` package (which uses `gjson`) rather than `json.Unmarshal(...)` into a struct. There's a few reasons for this:
 - Removes the temptation to use `gomatrixserverlib` structs.
 - Forces exact key matching (without JSON tags, `json.Unmarshal` will case-insensitively match fields by default).
 - Clearer syntax: `match.JSONKeyEqual("earliest_events", []interface{}{latestEvent.EventID()}),` is clearer than unmarshalling into a slice then doing assertions on the first element.

If you want to extract data from objects, just use `gjson` directly.

### How should I assert HTTP requests/responses?

Use the corresponding matcher in the `match` package. This allows you to be as specific or as lax as you like on your checks, and allows you to add JSON matchers on
the HTTP body.

### I want to run a bunch of tests in parallel, how do I do this?

This is done using the standard Go testing mechanisms. Add `t.Parallel()` to all tests which you want to run in parallel. For a good example of this, see `registration_test.go` which does:
```go
// This will block until the 2 subtests have completed
t.Run("parallel", func(t *testing.T) {
    // run a subtest
    t.Run("POST {} returns a set of flows", func(t *testing.T) {
        t.Parallel()
        ...
    })
    // run another subtest
    t.Run("POST /register can create a user", func(t *testing.T) {
        t.Parallel()
        ...
    })
})
```

### How should I do comments in the test?

Add long prose to the start of the function to outline what it is you're testing (and why if it is unclear). For example:
```go
// Test that a server can receive /keys requests:
// https://matrix.org/docs/spec/server_server/latest#get-matrix-key-v2-server-keyid
func TestInboundFederationKeys(t *testing.T) {
    ...
}
```

### I think Complement is doing something weird, can I get more logs?

You can pass `COMPLEMENT_DEBUG=1` to add lots of debug logging. You can also do this via `os.Setenv("COMPLEMENT_DEBUG", "1")` before you make a deployment. This will add trace logging to the clients which logs full HTTP request/responses, amongst other debug info.

### How do I set up a bunch of stuff before the tests, e.g before each?

There is no syntactically pleasing way to do this. Create a separate function which returns a function. See https://stackoverflow.com/questions/42310088/setup-and-teardown-for-each-test-using-std-testing-package?rq=1

### How do I log messages in tests?

This is done using standard Go testing mechanisms, use `t.Logf(...)` which will be logged only if the test fails or if `-v` is set. Note that you will not need to log HTTP requests performed using one of the built in deployment clients as they are already wrapped in loggers. For full HTTP logs, use `COMPLEMENT_DEBUG=1`.

For debugging, you can also use `logrus` to expand a bunch of variables:

```go
logrus.WithFields(logrus.Fields{
	"events": events,
	"context": context,
}).Error("message response")
```

### How do I show the server logs even when the tests pass?

Normally, server logs are only printed when one of the tests fail. To override that behavior to always show server logs, you can use `COMPLEMENT_ALWAYS_PRINT_SERVER_LOGS=1`.


### How do I skip a test?

Use one of `t.Skipf(...)` or `t.SkipNow()`.

### Why do we use `t.Errorf` sometimes and `t.Fatalf` other times?

Error will fail the test but continue execution, where Fatal will fail the test and quit. Use Fatal when continuing to run the test will result in programming errors (e.g nil exceptions).

### Why do I get the error "Error response from daemon: Conflict. The container name "/complement_rooms_state_alice.hs1_1" is already in use by container "c2d1d90c6cff7b7de2678b56c702bd1ff76ca72b930e8f2ca32eef3f2514ff3b". You have to remove (or rename) that container to be able to reuse that name."?

The Docker daemon has a lag time between removing containers and them actually being removed. This means you cannot remove a container called 'foo' and immediately recreate it as 'foo'. To get around this, you need to use a different name. This probably means the namespace you have given the deployment is used by another test. Try changing it to something else e.g `Deploy(t, "rooms_state_2", b.BlueprintAlice.Name)`

### How do I run tests inside my IDE?

For VSCode, add to `settings.json`:
```
"go.testEnvVars": {
    "COMPLEMENT_BASE_IMAGE": "complement-dendrite:latest"
}
```

For Goland:
 * Under "Run"->"Edit Configurations..."->"Templates"->"Go Test", add `COMPLEMENT_BASE_IMAGE=complement-dendrite:latest`
 * Then you can right-click on any test file or test case and "Run <test name>".


### How do I hook up a Matrix client like Element to the homeservers spun up by Complement after a test runs?

It can be useful to view the output of a test in Element to better debug something going wrong or just make sure your test is doing what you expect before you try to assert everything.

 1. In your test comment out `defer deployment.Destroy(t)` and replace with `defer time.Sleep(2 * time.Hour)` to keep the homeserver running after the tests complete
 1. Start the Complement tests
 1. Save the Element config as `~/Downloads/riot-complement-config.json` and replace the port according to the output from `docker ps` (`docker ps -f name=complement_` to just filter to the Complement containers)
    ```json
    {
      "default_server_config": {
        "m.homeserver": {
          "base_url": "http://localhost:55449",
          "server_name": "my.complement.host"
        }
      },
      "brand": "Element"
    }
    ```
 1. Start up Element (your matrix client)
    ```
    docker run -it --rm \
        --publish 7080:80 \
        --volume ~/Downloads/riot-complement-config.json:/app/config.json \
        --name riot-complement \
        vectorim/riot-web:v1.7.8
    ```
 1. Now you can visit http://localhost:7080/ and register a new user and explore the rooms from the test output


### What do I need to know if I'm coming from sytest?

Sytest has a concept of a `fixture` to configure the homeserver or test in a particular way, these are replaced with a `Blueprint` in Complement.

Unlike Sytest, each test must opt-in to attaching core functionality to the server so the reader can clearly see what is and is not being handled automatically.
