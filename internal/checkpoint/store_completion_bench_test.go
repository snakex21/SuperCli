package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The same 1,500-record metadata document and clean ledger are used for both
// paths. This measures the Manager + StoreGate path, not just the tiny counter.
// Fixture creation and initial physical census are outside the timer.
func BenchmarkManagedManagerCleanCompletion(b *testing.B) {
	for _, previous := range []bool{true, false} {
		name := "single_decode"
		if previous {
			name = "previous_double_decode"
		}
		b.Run(name, func(b *testing.B) {
			b.StopTimer()
			ctx := context.Background()
			m, err := Open(b.TempDir(), b.TempDir())
			if errors.Is(err, ErrUnavailable) {
				b.Skip(err)
			}
			if err != nil {
				b.Fatal(err)
			}
			records := make([]Record, 1500)
			for i := range records {
				records[i] = Record{ID: "fixture", SessionID: "session", UserSeq: i + 1, Before: strings.Repeat("a", 40), After: strings.Repeat("b", 40), Prompt: strings.Repeat("p", 512), Files: []string{"src/file.go"}, Changes: []FileChange{{Path: "src/file.go", Kind: "modified"}}, RawBytes: true}
			}
			encoded, err := json.MarshalIndent(records, "", "  ")
			if err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(m.meta, encoded, 0600); err != nil {
				b.Fatal(err)
			}
			if err := m.completeRetainedUsage(ctx, DefaultStoreBudgetBytes); err != nil {
				b.Fatal(err)
			}
			b.Setenv("PATH", b.TempDir())
			b.ReportAllocs()
			b.ResetTimer()
			b.StartTimer()
			for i := 0; i < b.N; i++ {
				if previous {
					// Previous completion always decoded again even after the clean exit.
					unlock, err := m.lockStore(ctx)
					if err != nil {
						b.Fatal(err)
					}
					counter, err := m.usageCounterLocked()
					if err != nil {
						b.Fatal(err)
					}
					completion, counterErr := counter.CompleteManagedLocked(ctx, DefaultStoreBudgetBytes, nil)
					reloadErr := m.reloadRecordsLocked()
					closeErr := unlock()
					if err := errors.Join(counterErr, reloadErr, closeErr); err != nil || completion.Collected {
						b.Fatalf("baseline: %+v %v", completion, err)
					}
				} else if err := m.completeRetainedUsage(ctx, DefaultStoreBudgetBytes); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if len(m.records) != len(records) {
				b.Fatal("metadata lost")
			}
			raw, err := os.ReadFile(m.meta)
			if err != nil || string(raw) != string(encoded) {
				b.Fatal("clean completion changed metadata:", err)
			}
			if _, err := os.Lstat(filepath.Join(filepath.Dir(m.meta), retentionExpiryName)); !os.IsNotExist(err) {
				b.Fatal("unexpected collection")
			}
			b.ReportMetric(float64(len(encoded)), "metadata_bytes")
		})
	}
}
