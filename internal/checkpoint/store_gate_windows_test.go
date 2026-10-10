//go:build windows

package checkpoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreGateWindowsLongPortablePath(t *testing.T) {
	data := t.TempDir()
	for len(data) < 280 {
		data = filepath.Join(data, strings.Repeat("portable-", 4))
	}
	if err := os.MkdirAll(data, 0700); err != nil {
		t.Fatal(err)
	}
	gate := newTestStoreGate(t, data)
	lease := mustAcquireStoreGate(t, gate)
	requireStoreBusy(t, newTestStoreGate(t, data))
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	mustAcquireStoreGate(t, gate)
}

func TestStoreGateWindowsLocalTokenFoldsPathCase(t *testing.T) {
	data := filepath.Join(t.TempDir(), "PortableData")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	a := newTestStoreGate(t, data)
	b := newTestStoreGate(t, strings.ToUpper(data))
	if a.local != b.local {
		t.Fatal("case aliases did not share a local token")
	}
	mustAcquireStoreGate(t, a)
	requireStoreBusy(t, b)
}
