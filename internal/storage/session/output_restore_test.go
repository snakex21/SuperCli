package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"supercli/internal/tools/core"
)

func TestToolOutputReadPreservesAllBytes(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create("fixture", "model", "")
	if err != nil {
		t.Fatal(err)
	}
	writer := NewWriter(store, sess.ID)
	for i, text := range []string{"", "plain", "Żółw 🙂", "nul\x00bytes\xff\xfe", "legacy-\xb9\xea", strings.Repeat("🙂", 10000)} {
		h := fmt.Sprintf("out_bytes_%d", i)
		if err := writer.SaveToolOutput(context.Background(), h, text); err != nil {
			t.Fatal(err)
		}
		got, err := writer.ReadToolOutput(context.Background(), h)
		if err != nil || got != text {
			t.Fatalf("case=%d length=%d err=%v", i, len(got), err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := writer.ReadToolOutput(ctx, "out_bytes_1"); err == nil {
		t.Fatal("canceled read succeeded")
	}
}

type countedOutputReader struct {
	writer *Writer
	reads  atomic.Int64
}

func (r *countedOutputReader) SaveToolOutput(ctx context.Context, h, text string) error {
	return r.writer.SaveToolOutput(ctx, h, text)
}
func (r *countedOutputReader) ReadToolOutput(ctx context.Context, h string) (string, error) {
	r.reads.Add(1)
	return r.writer.ReadToolOutput(ctx, h)
}

func BenchmarkToolOutputRestore(b *testing.B) {
	store, err := OpenStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create("fixture", "model", "")
	if err != nil {
		b.Fatal(err)
	}
	writer := NewWriter(store, sess.ID)
	for _, size := range []int{9 * 1024, 1024 * 1024} {
		h := fmt.Sprintf("out_bench_%d", size)
		text := strings.Repeat("a", size)
		if err := writer.SaveToolOutput(context.Background(), h, text); err != nil {
			b.Fatal(err)
		}
		for _, mode := range []string{"database", "cold_one", "cold_eight", "warm_eight"} {
			b.Run(fmt.Sprintf("%d/%s", size, mode), func(b *testing.B) {
				backend := &countedOutputReader{writer: writer}
				ctx := core.WithOutputPersistence(context.Background(), backend)
				shared := core.NewOutputStore()
				args, _ := json.Marshal(map[string]any{"handle": h, "limit": 128})
				if mode == "warm_eight" {
					result, err := shared.ReadOutputTool().Fn(ctx, args)
					if err != nil || result.Err != nil {
						b.Fatal(err, result.Err)
					}
					backend.reads.Store(0)
				}
				b.ReportAllocs()
				for b.Loop() {
					if mode == "database" {
						got, err := backend.ReadToolOutput(ctx, h)
						if err != nil || got != text {
							b.Fatalf("bad result %v", err)
						}
						continue
					}
					cache := shared
					if mode != "warm_eight" {
						cache = core.NewOutputStore()
					}
					tool := cache.ReadOutputTool()
					n := 8
					if mode == "cold_one" {
						n = 1
					}
					var wg sync.WaitGroup
					errs := make(chan error, n)
					start := make(chan struct{})
					for i := 0; i < n; i++ {
						wg.Add(1)
						go func() {
							defer wg.Done()
							<-start
							result, err := tool.Fn(ctx, args)
							if err == nil {
								err = result.Err
							}
							if err == nil && !strings.Contains(result.Text, strings.Repeat("a", 128)) {
								err = fmt.Errorf("missing chunk")
							}
							errs <- err
						}()
					}
					close(start)
					wg.Wait()
					close(errs)
					for err := range errs {
						if err != nil {
							b.Fatal(err)
						}
					}
				}
				b.ReportMetric(float64(backend.reads.Load())/float64(b.N), "db_reads/op")
			})
		}
	}
}
