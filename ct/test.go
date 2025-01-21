// package ct contains wrappers and interfaces around testing.T
//
// The intention is that _all_ complement functions deal with these wrapper interfaces
// rather than the literal testing.T. This enables Complement to be run in environments
// that aren't strictly via `go test`.
package ct

// TestLike is an interface that testing.T satisfies. All client functions accept a TestLike interface,
// with the intention of a `testing.T` being passed into them. However, the client may be used in non-test
// scenarios e.g benchmarks, which can then use the same client by just implementing this interface.
type TestLike interface {
	Helper()
	Logf(msg string, args ...interface{})
	Skipf(msg string, args ...interface{})
	Error(args ...interface{})
	Errorf(msg string, args ...interface{})
	Fatalf(msg string, args ...interface{})
	Failed() bool
	Name() string
}

const ansiRedForeground = "\x1b[31m"
const ansiResetForeground = "\x1b[39m"

// Errorf is a wrapper around t.Errorf which prints the failing error message in red.
func Errorf(t TestLike, format string, args ...any) {
	t.Helper()
	format = ansiRedForeground + format + ansiResetForeground
	t.Errorf(format, args...)
}

// Fatalf is a wrapper around t.Fatalf which prints the failing error message in red.
func Fatalf(t TestLike, format string, args ...any) {
	t.Helper()
	format = ansiRedForeground + format + ansiResetForeground
	t.Fatalf(format, args...)
}
