package checkpoint

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Measure one completion with 64 changed 1 KiB files and an unrelated file in
// the original snapshots. Git initialization, captures and cleanup stay outside
// the timer. Each iteration removes only the four newly generated flat tree and
// commit objects, so deduplication cannot hide their compression/write cost.
// The central runner supplies portable TMP/TEMP; all state stays in b.TempDir.
func BenchmarkNarrowChangedSnapshots64(b *testing.B) {
	b.StopTimer()
	root := b.TempDir()
	home := filepath.Join(root, "workspace")
	if err := os.MkdirAll(home, 0o700); err != nil {
		b.Fatal(err)
	}
	m, err := Open(home, filepath.Join(root, "data"))
	if errors.Is(err, ErrUnavailable) {
		b.Skip(err)
	}
	if err != nil {
		b.Fatal(err)
	}
	const count, size = 64, 1024
	files := make([]string, count)
	writePhase := func(phase string) {
		for i := range files {
			name := fmt.Sprintf("asset-%03d.bin", i)
			files[i] = name
			body := bytes.Repeat([]byte("a"), size)
			copy(body, fmt.Sprintf("synthetic %s blob %03d\n", phase, i))
			if err := os.WriteFile(filepath.Join(home, name), body, 0o600); err != nil {
				b.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(home, "unrelated.bin"), []byte("excluded from the completed record"), 0o600); err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	writePhase("before")
	before, err := m.captureSnapshot(ctx, nil, "", nil)
	if err != nil {
		b.Fatal(err)
	}
	writePhase("after")
	after, err := m.captureSnapshot(ctx, nil, "", nil)
	if err != nil {
		b.Fatal(err)
	}
	minimalBefore, minimalAfter, err := m.narrowChangedSnapshots(ctx, before, after, files)
	if err != nil {
		b.Fatal(err)
	}
	generated := make(map[string]bool)
	for _, commit := range []string{minimalBefore, minimalAfter} {
		tree, err := m.git(ctx, "rev-parse", commit+"^{tree}")
		if err != nil {
			b.Fatal(err)
		}
		for _, oid := range []string{commit, strings.TrimSpace(tree)} {
			if !validNarrowOID(oid) {
				b.Fatal("invalid benchmark object OID")
			}
			path := filepath.Join(m.repo, "objects", oid[:2], oid[2:])
			if !within(root, path) {
				b.Fatal("benchmark object escapes its fixture root")
			}
			generated[path] = true
		}
	}
	if len(generated) != 4 {
		b.Fatalf("expected four distinct generated objects, got %d", len(generated))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Only fixed, validated object files in this benchmark's own store.
		for path := range generated {
			if err := os.Remove(path); err != nil {
				b.Fatal(err)
			}
		}
		b.StartTimer()
		gotBefore, gotAfter, err := m.narrowChangedSnapshots(ctx, before, after, files)
		if err != nil || gotBefore != minimalBefore || gotAfter != minimalAfter {
			b.Fatalf("narrowing failed: before=%s after=%s err=%v", gotBefore, gotAfter, err)
		}
	}
	b.StopTimer()
	b.ReportMetric(count, "files/op")
}
