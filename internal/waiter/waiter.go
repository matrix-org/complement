package waiter

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

type Waiter struct {
	mu     sync.Mutex
	ch     chan bool
	closed bool
}

// NewWaiter returns a generic struct which can be waited on until `Waiter.Finish` is called.
// A Waiter is similar to a `sync.WaitGroup` of size 1, but without the ability to underflow and
// with built-in timeouts.
func New() *Waiter {
	return &Waiter{
		ch: make(chan bool),
		mu: sync.Mutex{},
	}
}

// Wait blocks until Finish() is called or until the timeout is reached.
// If the timeout is reached, the test is failed.
func (w *Waiter) Wait(t *testing.T, timeout time.Duration) {
	t.Helper()
	w.Waitf(t, timeout, "Wait")
}

// Waitf blocks until Finish() is called or until the timeout is reached.
// If the timeout is reached, the test is failed with the given error message.
func (w *Waiter) Waitf(t *testing.T, timeout time.Duration, errFormat string, args ...interface{}) {
	t.Helper()
	select {
	case <-w.ch:
		return
	case <-time.After(timeout):
		errmsg := fmt.Sprintf(errFormat, args...)
		t.Fatalf("%s: timed out after %f seconds.", errmsg, timeout.Seconds())
	}
}

// Finish will cause all goroutines waiting via Wait to stop waiting and return.
// Once this function has been called, subsequent calls to Wait will return immediately.
// To begin waiting again, make a new Waiter.
func (w *Waiter) Finish() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	close(w.ch)
}
