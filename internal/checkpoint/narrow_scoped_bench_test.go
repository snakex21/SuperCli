package checkpoint

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Same already-scoped before/after inputs for both helper variants. The caller's
// common StoreGate and accounting transaction are outside this step's timer.
// This does not claim the speedup of a full task, capture, or provider request.
func BenchmarkRecordSnapshotsScoped64(b *testing.B) {
	for _, legacy := range []bool{true, false} {
		name := "fast_proof"
		if legacy {
			name = "existing_narrow"
		}
		b.Run(name, func(b *testing.B) {
			b.StopTimer()
			ctx := context.Background()
			home, data := b.TempDir(), b.TempDir()
			m, err := Open(home, data)
			if err != nil {
				b.Fatal(err)
			}
			files := make([]string, 64)
			writePhase := func(tag string) {
				for i := range files {
					files[i] = fmt.Sprintf("file-%03d.bin", i)
					body := bytes.Repeat([]byte("a"), 1024)
					copy(body, fmt.Sprintf("synthetic %s %d", tag, i))
					if err := os.WriteFile(filepath.Join(home, files[i]), body, 0600); err != nil {
						b.Fatal(err)
					}
				}
			}
			writePhase("before")
			before, err := m.captureSnapshot(ctx, files, "", nil)
			if err != nil {
				b.Fatal(err)
			}
			writePhase("after")
			after, err := m.captureSnapshot(ctx, files, "", nil)
			if err != nil {
				b.Fatal(err)
			}
			turn := m.NewTurn("synthetic", "scoped benchmark")
			turn.touched, turn.scopeRoots = true, files
			unlock, err := m.lockStore(ctx)
			if err != nil {
				b.Fatal(err)
			}
			defer func() {
				if err := unlock(); err != nil {
					b.Error(err)
				}
			}()
			turn.mu.Lock()
			defer turn.mu.Unlock()

			b.ReportAllocs()
			b.ResetTimer()
			b.StartTimer()
			for i := 0; i < b.N; i++ {
				if legacy {
					gotBefore, gotAfter, err := m.narrowChangedSnapshotsLocked(ctx, before, after, files)
					if err != nil || gotBefore != before || gotAfter != after {
						b.Fatal("already scoped inputs changed", err)
					}
				} else {
					gotBefore, gotAfter, err := turn.recordSnapshotsLocked(ctx, before, after, files)
					if err != nil || gotBefore != before || gotAfter != after {
						b.Fatal("fast proof changed snapshots", err)
					}
				}
			}
			b.StopTimer()
			b.ReportMetric(64, "files/op")
			if legacy {
				b.ReportMetric(2, "gitcalls/op")
			} else {
				b.ReportMetric(0, "gitcalls/op")
			}
		})
	}
}
