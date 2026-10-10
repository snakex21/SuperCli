package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newUsageCounterFixture(t *testing.T) (*StoreGate, *StoreUsageCounter) {
	t.Helper()
	gate, err := NewStoreGate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	counter, err := NewStoreUsageCounter(gate)
	if err != nil {
		t.Fatal(err)
	}
	return gate, counter
}

func withUsageCounterGate(t *testing.T, gate *StoreGate, fn func()) {
	t.Helper()
	lease, err := gate.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lease.Close(); err != nil {
			t.Error(err)
		}
	}()
	fn()
}

func usageFixtureCensus(ctx context.Context, dataDir string) (int64, error) {
	var total int64
	for _, name := range []string{"checkpoints", "badcheckpoints"} {
		root := filepath.Join(dataDir, name)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if os.IsNotExist(err) && path == root {
				return fs.SkipDir
			}
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return ErrStoreInventory
			}
			total, err = retentionAdd(total, info.Size())
			return err
		})
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

func TestStoreUsageCounterMonotoneFastpathAndPressure(t *testing.T) {
	gate, c := newUsageCounterFixture(t)
	withUsageCounterGate(t, gate, func() {
		ctx := context.Background()
		calls := 0
		collect := func(ctx context.Context) (int64, error) {
			calls++
			return usageFixtureCensus(ctx, c.dataDir)
		}
		first, err := c.CompleteLocked(ctx, 500, collect)
		if err != nil || !first.Collected || calls != 1 || first.UpperBytes != 0 {
			t.Fatalf("first completion: %+v calls=%d err=%v", first, calls, err)
		}
		w, err := c.BeginLocked(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state, err := c.readLocked()
		if err != nil || !state.Dirty {
			t.Fatal("writes admitted before durable dirty marker")
		}
		root := filepath.Join(c.dataDir, "checkpoints", "synthetic")
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		body := []byte("new compressed object bytes")
		object := filepath.Join(root, "new-object")
		if err := os.WriteFile(object, body, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := w.AddPublishedBlobBytes(int64(len(body))); err != nil {
			t.Fatal(err)
		}
		if err := w.FinishLocked(ctx, StoreUsageGrowth{RefBytes: 41, MetadataBytes: 64}); err != nil {
			t.Fatal(err)
		}
		expected := int64(len(body)) + 41 + 64
		for i := 0; i < 4; i++ {
			got, err := c.CompleteLocked(ctx, 500, collect)
			if err != nil || got.Collected || got.UpperBytes != expected || calls != 1 {
				t.Fatalf("fastpath walked store: %+v calls=%d err=%v", got, calls, err)
			}
		}
		// Deletion is not subtracted. A successful dedup/no-op transaction adds
		// zero blob bytes and does not itself invoke graph work.
		w, err = c.BeginLocked(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(object); err != nil {
			t.Fatal(err)
		}
		if err := w.FinishLocked(ctx, StoreUsageGrowth{}); err != nil {
			t.Fatal(err)
		}
		got, err := c.CompleteLocked(ctx, expected-1, collect)
		if err != nil || !got.Collected || got.UpperBytes != 0 || calls != 2 {
			t.Fatalf("pressure did not recensus confirmed deletion: %+v calls=%d err=%v", got, calls, err)
		}
	})
}

func TestStoreUsageCounterPriorDirtyCannotBeWashedOut(t *testing.T) {
	gate, first := newUsageCounterFixture(t)
	second, err := NewStoreUsageCounter(gate)
	if err != nil {
		t.Fatal(err)
	}
	withUsageCounterGate(t, gate, func() {
		ctx := context.Background()
		if _, err := first.CompleteLocked(ctx, 500, func(context.Context) (int64, error) { return 0, nil }); err != nil {
			t.Fatal(err)
		}
		abandoned, err := first.BeginLocked(ctx)
		if err != nil {
			t.Fatal(err)
		}
		w, err := second.BeginLocked(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.AddPublishedBlobBytes(17); err != nil {
			t.Fatal(err)
		}
		if err := w.FinishLocked(ctx, StoreUsageGrowth{}); err != nil {
			t.Fatal(err)
		}
		state, err := second.readLocked()
		if err != nil || !state.Dirty || state.Upper != 17 {
			t.Fatalf("cached writer erased crashed writer debt: %+v %v", state, err)
		}
		if err := abandoned.FinishLocked(ctx, StoreUsageGrowth{}); !errors.Is(err, ErrStoreUsageCensus) {
			t.Fatalf("stale receipt rewrote newer shared state: %v", err)
		}
		got, err := second.CompleteLocked(ctx, 500, func(context.Context) (int64, error) { return 37, nil })
		if err != nil || !got.Collected || got.UpperBytes != 37 {
			t.Fatalf("dirty recovery did not census: %+v %v", got, err)
		}
	})
}

func TestStoreUsageCounterFailuresRemainDirty(t *testing.T) {
	for _, scenario := range []string{"canceled-finish", "bad-growth", "bad-finish-bound", "overflow", "unknown-init", "collector-failure"} {
		t.Run(scenario, func(t *testing.T) {
			gate, c := newUsageCounterFixture(t)
			withUsageCounterGate(t, gate, func() {
				ctx := context.Background()
				if _, err := c.CompleteLocked(ctx, 500, func(context.Context) (int64, error) { return 0, nil }); err != nil {
					t.Fatal(err)
				}
				w, err := c.BeginLocked(ctx)
				if err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "canceled-finish":
					canceled, cancel := context.WithCancel(ctx)
					cancel()
					err = w.FinishLocked(canceled, StoreUsageGrowth{})
				case "bad-growth":
					if err := w.AddPublishedBlobBytes(10); err != nil {
						t.Fatal(err)
					}
					if err := w.AddPublishedBlobBytes(-1); err == nil {
						t.Fatal("negative delta admitted")
					}
					err = w.FinishLocked(ctx, StoreUsageGrowth{})
				case "bad-finish-bound":
					err = w.FinishLocked(ctx, StoreUsageGrowth{MetadataBytes: -1})
				case "overflow":
					if err := w.AddPublishedBlobBytes(math.MaxInt64); err != nil {
						t.Fatal(err)
					}
					err = w.FinishLocked(ctx, StoreUsageGrowth{RefBytes: 41})
				case "unknown-init":
					if err := w.RequireCensus(); err != nil {
						t.Fatal(err)
					}
					err = w.FinishLocked(ctx, StoreUsageGrowth{MetadataBytes: 20})
				case "collector-failure":
					_, err = c.CompleteLocked(ctx, 500, func(context.Context) (int64, error) { return 0, errors.New("synthetic collector failure") })
				}
				if err == nil && scenario != "unknown-init" {
					t.Fatal("failed operation marked successful")
				}
				if scenario != "unknown-init" && scenario != "collector-failure" {
					if err := w.FinishLocked(ctx, StoreUsageGrowth{}); err == nil {
						t.Fatal("failed receipt retried into clean state")
					}
				}
				state, readErr := c.readLocked()
				if readErr != nil || !state.Dirty {
					t.Fatalf("failure cleared durable dirty: %+v %v", state, readErr)
				}
			})
		})
	}
}

func TestStoreUsageCounterUnsafeMarkerBlocksBegin(t *testing.T) {
	gate, c := newUsageCounterFixture(t)
	withUsageCounterGate(t, gate, func() {
		path := filepath.Join(c.dataDir, storeUsageName)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if w, err := c.BeginLocked(context.Background()); err == nil || w != nil {
			t.Fatal("checkpoint writes admitted without a durable marker")
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatal("unsafe marker path was replaced")
		}
	})
}

func TestStoreUsageCounterConcurrentCachedWritersShareFreshState(t *testing.T) {
	gate, a := newUsageCounterFixture(t)
	b, err := NewStoreUsageCounter(gate)
	if err != nil {
		t.Fatal(err)
	}
	var censusCalls atomic.Int32
	collect := func(context.Context) (int64, error) { censusCalls.Add(1); return 0, nil }
	withUsageCounterGate(t, gate, func() {
		if _, err := a.CompleteLocked(context.Background(), DefaultStoreBudgetBytes, collect); err != nil {
			t.Fatal(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start, done := make(chan struct{}), make(chan error, 2)
	const transactions = 16
	for _, c := range []*StoreUsageCounter{a, b} {
		go func(c *StoreUsageCounter) {
			<-start
			for i := 0; i < transactions; i++ {
				lease, err := gate.Acquire(ctx)
				if err != nil {
					done <- err
					return
				}
				err = func() error {
					w, err := c.BeginLocked(ctx)
					if err != nil {
						return err
					}
					if err := w.AddPublishedBlobBytes(13); err != nil {
						return err
					}
					if err := w.FinishLocked(ctx, StoreUsageGrowth{RefBytes: 41, MetadataBytes: 23}); err != nil {
						return err
					}
					result, err := c.CompleteLocked(ctx, DefaultStoreBudgetBytes, collect)
					if result.Collected {
						return errors.New("clean cached transaction invoked census")
					}
					return err
				}()
				err = errors.Join(err, lease.Close())
				if err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}(c)
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
	withUsageCounterGate(t, gate, func() {
		s, err := a.readLocked()
		if err != nil || s.Dirty || s.Upper != 2*transactions*(13+41+23) || censusCalls.Load() != 1 {
			t.Fatalf("lost shared increments: %+v census=%d err=%v", s, censusCalls.Load(), err)
		}
	})
}

func TestStoreUsageCounterRealProcessCrashAndFreshProcessCensus(t *testing.T) {
	gate, c := newUsageCounterFixture(t)
	withUsageCounterGate(t, gate, func() {
		if _, err := c.CompleteLocked(context.Background(), 500, func(context.Context) (int64, error) { return 0, nil }); err != nil {
			t.Fatal(err)
		}
	})
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := func(mode string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, "-test.run=^TestStoreUsageCounterProcessHelper$")
		cmd.Env = append(os.Environ(), "SUPERCLI_USAGE_COUNTER_CHILD="+mode, "SUPERCLI_USAGE_COUNTER_FIXTURE="+c.dataDir)
		out, err := cmd.CombinedOutput() // One blocking completion, no polling.
		if err != nil || !strings.Contains(string(out), "synthetic counter child ready") {
			t.Fatalf("child %s: %v %s", mode, err, out)
		}
	}
	child("first-completion") // Clean parent ledger cannot skip new-process census.
	child("crash")            // os.Exit without Finish or gate Close.
	withUsageCounterGate(t, gate, func() {
		s, err := c.readLocked()
		if err != nil || !s.Dirty {
			t.Fatalf("child crash lost dirty marker: %+v %v", s, err)
		}
		w, err := c.BeginLocked(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := w.FinishLocked(context.Background(), StoreUsageGrowth{}); err != nil {
			t.Fatal(err)
		}
		s, err = c.readLocked()
		if err != nil || !s.Dirty {
			t.Fatal("parent completion washed out crashed child")
		}
		result, err := c.CompleteLocked(context.Background(), 500, func(ctx context.Context) (int64, error) { return usageFixtureCensus(ctx, c.dataDir) })
		if err != nil || !result.Collected || result.UpperBytes != int64(len("synthetic crash bytes")) {
			t.Fatalf("crash recovery: %+v %v", result, err)
		}
	})
}

func TestStoreUsageCounterProcessHelper(t *testing.T) {
	mode, fixture := os.Getenv("SUPERCLI_USAGE_COUNTER_CHILD"), os.Getenv("SUPERCLI_USAGE_COUNTER_FIXTURE")
	if mode == "" {
		return
	}
	if !filepath.IsAbs(fixture) {
		t.Fatal("absolute synthetic fixture required")
	}
	gate, err := NewStoreGate(fixture)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewStoreUsageCounter(gate)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := gate.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if mode == "first-completion" {
		calls := 0
		result, err := c.CompleteLocked(context.Background(), 500, func(ctx context.Context) (int64, error) { calls++; return usageFixtureCensus(ctx, fixture) })
		if err != nil || !result.Collected || calls != 1 {
			t.Fatalf("fresh process trusted old ledger: %+v %d %v", result, calls, err)
		}
	} else if mode == "crash" {
		if _, err := c.BeginLocked(context.Background()); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(fixture, "checkpoints", "synthetic")
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "crash-object"), []byte("synthetic crash bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Fatal("unknown synthetic child mode")
	}
	fmt.Println("synthetic counter child ready")
	os.Exit(0) // Deliberately skip deferred Close/Finish; OS releases native gate.
}
