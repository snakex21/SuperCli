package checkpoint

import (
	"os"
	"strconv"
	"testing"
	"time"
)

// nativeProcSlots bounds how many parallel tests spawn native processes (git,
// re-exec of the test binary) at once. Unbounded process fan-out on loaded
// machines (Defender, CI) turned wall-clock assertions into flakes.
var nativeProcSlots = make(chan struct{}, nativeProcLimit())

func nativeProcLimit() int {
	if n, err := strconv.Atoi(os.Getenv("SUPERCLI_TEST_NATIVE_PROCS")); err == nil && n > 0 {
		return n
	}
	return 2
}

// parallelNativeTest marks a top-level test parallel and then holds one native
// process slot until the test (and its cleanups) finish. Only call it from
// top-level tests: a parent holding a slot while parallel subtests wait for
// one would deadlock.
func parallelNativeTest(t *testing.T) {
	t.Helper()
	t.Parallel()
	nativeProcSlots <- struct{}{}
	t.Cleanup(func() { <-nativeProcSlots })
}

// skipSlowNativeInShort skips heavy native-process tests (console sessions,
// job objects, re-exec fixtures) under -short; plain `go test` still runs them.
func skipSlowNativeInShort(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("slow native-process test; skipped with -short")
	}
}

// testTimeoutScale multiplies wall-clock test bounds on slow machines via
// SUPERCLI_TEST_TIMEOUT_SCALE (default 1).
func testTimeoutScale() float64 {
	if f, err := strconv.ParseFloat(os.Getenv("SUPERCLI_TEST_TIMEOUT_SCALE"), 64); err == nil && f >= 1 {
		return f
	}
	return 1
}

func scaledTestTimeout(d time.Duration) time.Duration {
	return time.Duration(float64(d) * testTimeoutScale())
}

// setCheckpointOutputDrainGrace pins the drain grace for one non-parallel test.
func setCheckpointOutputDrainGrace(t *testing.T, grace time.Duration) {
	t.Helper()
	previous := checkpointOutputDrainGraceNanos.Swap(int64(grace))
	t.Cleanup(func() { checkpointOutputDrainGraceNanos.Store(previous) })
}
