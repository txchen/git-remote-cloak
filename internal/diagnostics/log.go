// Package diagnostics emits opt-in, secret-free operation timings to stderr.
package diagnostics

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	started = time.Now()
	mu      sync.Mutex
)

// Enabled reports whether the caller requested diagnostic output.
func Enabled() bool { return os.Getenv("CLOAK_LOG") == "debug" }

// Event records a fixed operation milestone. Callers must not pass user input,
// Git arguments, paths, object IDs, or credentials as the event name.
func Event(name string) {
	if Enabled() {
		write(name)
	}
}

// Count records an aggregate count without exposing object identities.
func Count(name string, value int) {
	if Enabled() {
		write(fmt.Sprintf("%s=%d", name, value))
	}
}

// Stage records the start and elapsed duration of a fixed operation stage.
// The returned function should be deferred immediately at the stage boundary.
func Stage(name string) func() {
	if !Enabled() {
		return func() {}
	}
	start := time.Now()
	write(name + " started")
	return func() {
		write(fmt.Sprintf("%s ended after %s", name, time.Since(start).Round(time.Millisecond)))
	}
}

func write(message string) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintf(os.Stderr, "cloak debug +%s: %s\n", time.Since(started).Round(time.Millisecond), message)
}
