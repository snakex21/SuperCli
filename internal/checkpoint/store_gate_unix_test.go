//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package checkpoint

import (
	"context"
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestStoreGateRejectsFIFOWithoutOpeningIt(t *testing.T) {
	gate := newTestStoreGate(t, t.TempDir())
	if err := unix.Mkfifo(gate.path, 0600); err != nil {
		t.Fatal(err)
	}
	if lease, err := gate.TryAcquire(context.Background()); lease != nil || err == nil || errors.Is(err, ErrStoreBusy) {
		t.Fatalf("FIFO accepted or confused with busy: lease=%v, error=%v", lease, err)
	}
	info, err := os.Lstat(gate.path)
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("FIFO was replaced: %v", err)
	}
}

func TestStoreGateCreatesPrivateUnixLock(t *testing.T) {
	gate := newTestStoreGate(t, t.TempDir())
	lease := mustAcquireStoreGate(t, gate)
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(gate.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Fatalf("lock grants group or other access: %v", info.Mode())
	}
}
