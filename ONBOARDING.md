### So you want to write Complement tests

If you've not run Complement tests yet, please do so. This document will outline how Complement works and how you can add efficient tests and best practices for Complement itself.

#### Architecture

Complement runs Docker containers every time you call `deployment := must.Deploy(...)`, which gets killed on `deployment.Destroy(...)`. These containers are snapshots of the target Homeserver at a particular state. The state is determined by the `Blueprint`, which is a human-readable outline for what should be done prior to the test. Coming from Sytest, a `Blueprint` is similar to a `fixture`. A `deployment` has functions on it for creating deployment-scoped structs such as CSAPI/SSAPI clients for interacting with specific Homeservers in the deployment. Assertions are done via the `must` package. For testing outbound federation, Complement implements a bare-bones Federation server for Homeservers to talk to. Unlike Sytest, you have to explicitly opt-in to attaching core functionality to the server so the reader can clearly see what is and is not being handled automatically. This is done using [functional options](https://dave.cheney.net/2014/10/17/functional-options-for-friendly-apis) and looks something like:
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
defer cancel()
```

Network topology for all forms of communication look like this:
```
+------+                  Outbound federation           +-----------+             Network: one per blueprint                +-----------+
|      | :35372 <------ host.docker.internal:35372 ---- |           |                                                       |           |
| Host |                                                | Container | ------- SS API https://hs2/_matrix/federation/... --> | Container |
|      | -------CS API http://localhost:38295 --------> |   (hs1)   |                                                       |   (hs2)   |
|      | ------SS API https://localhost:37562 --------> |           |                                                       |           |
+------+                                                +-----------+                                                       +-----------+
                                             docker -p 38295:8008 -p 37562:8448

The high numbered ports are randomly chosen, and are for illustrative purposes only.
```
The mapping of `hs1` to `localhost:port` combinations can be done automatically using a `docker.RoundTripper`.

#### FAQ

- Should I always make a new blueprint for a test?

Probably not. Blueprints are costly, and they should only be made if there is a strong case for plenty of reuse among tests. In the same way that we don't always add fixtures to sytest, we should be sparing with adding blueprints.

- I want to run a bunch of tests in parallel, how do I do this?

If you're familiar with Go testing then you already know how. Add `t.Parallel()` to all tests which you want to run in parallel. For a good example of this, see `registration_test.go` which does:
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

- How should I do comments in the test?

Add long prose to the start of the function to outline what it is you're testing (and why if it is unclear). For example:
```go
// Test that a server can receive /keys requests:
// https://matrix.org/docs/spec/server_server/latest#get-matrix-key-v2-server-keyid
func TestInboundFederationKeys(t *testing.T) {
    ...
}
```

- I think Complement is doing something weird, can I get more logs?

You can pass `COMPLEMENT_DEBUG=1` to add lots of debug logging.

- How do I set up a bunch of stuff before the tests, e.g before each?

There is no syntactically pleasing way to do this. Create a separate function which returns a function. See https://stackoverflow.com/questions/42310088/setup-and-teardown-for-each-test-using-std-testing-package?rq=1

- How do I log messages in tests?

Standard Go testing here, use `t.Logf(...)` which will be logged only if the test fails or if `-v` is set. Note that you will not need to log HTTP requests performed using one of the built in deployment clients as they are already wrapped in loggers.

- How do I skip a test?

Use one of `t.Skipf(...)` or `t.SkipNow()`.

- Why do we use `t.Errorf` sometimes and `t.Fatalf` other times?

Error will fail the test but continue execution, where Fatal will fail the test and quit. Use Fatal when continuing to run the test will result in programming errors (e.g nil exceptions).
