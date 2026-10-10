//go:build !windows && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package checkpoint

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestStoreGateUnsupportedFailsWithoutCreatingLock(t *testing.T) {
	gate, err := NewStoreGate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if lease, err := gate.TryAcquire(context.Background()); lease != nil || !errors.Is(err, ErrStoreUnsupported) {
		t.Fatalf("unsupported gate did not refuse: lease=%v, error=%v", lease, err)
	}
	if _, err := os.Stat(gate.path); !os.IsNotExist(err) {
		t.Fatalf("unsupported gate created a lock file: %v", err)
	}
}
