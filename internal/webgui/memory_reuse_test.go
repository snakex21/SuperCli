package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/storage/memory"
	"supercli/internal/tools"
)

func webMemoryAccessFixture(t testing.TB) (*Engine, *tools.Registry, []*memory.Store) {
	t.Helper()
	eng, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	global, project := eng.webMemoryStores(eng.Home())
	if global == nil || project == nil {
		t.Fatal("open fixture memory")
	}
	for index, store := range []*memory.Store{global, project} {
		n := 7
		if index == 1 {
			n = 49
		}
		for i := 0; i < n; i++ {
			scope := memory.ScopeFact
			if i%7 == 0 {
				scope = fmt.Sprintf("pattern:%d", i)
			} else if i%3 == 0 {
				scope = memory.ScopeTaskLog
			}
			if err := store.Put(memory.Entry{ID: fmt.Sprintf("fixture-%d", i), Scope: scope, Content: fmt.Sprintf("needle entry %d: keep portable project configuration beside the executable", i), Source: memory.SourceAgent}); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, reg, err := eng.buildLoopWithSession(nil, nil, eng.Home(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reg == nil {
		t.Fatal("missing loop registry")
	}
	return eng, reg, []*memory.Store{global, project}
}

func TestWebMemoryReadsDoNotRewriteMirrors(t *testing.T) {
	eng, reg, stores := webMemoryAccessFixture(t)
	for _, operation := range []string{"hit", "fallback", "no-user-facts"} {
		t.Run(operation, func(t *testing.T) {
			stamps := map[string]time.Time{}
			for _, store := range stores {
				path, _, err := memory.ScopeFile(filepath.Join(store.Root(), "memory"), memory.ScopeFact)
				if err != nil {
					t.Fatal(err)
				}
				stamp := time.Unix(1234567890, 0)
				if err := os.Chtimes(path, stamp, stamp); err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				stamps[path] = info.ModTime()
			}
			if operation == "no-user-facts" {
				eng.saveWebUserFacts("hello")
			} else {
				query := "needle"
				if operation == "fallback" {
					query = "unmatchedquery"
				}
				args, _ := json.Marshal(map[string]any{"query": query, "limit": 5})
				result, err := reg.Execute(context.Background(), "recall", args)
				if err != nil || result.Err != nil || !strings.Contains(result.Text, "portable") {
					t.Fatalf("recall: %v / %+v", err, result)
				}
			}
			for path, stamp := range stamps {
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if !info.ModTime().Equal(stamp) {
					t.Errorf("read-only %s rewrote memory mirror %s", operation, filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path))
				}
			}
		})
	}
}

func BenchmarkWebMemoryAccess(b *testing.B) {
	for _, operation := range []string{"hit", "fallback", "no-user-facts"} {
		b.Run(operation, func(b *testing.B) {
			eng, reg, _ := webMemoryAccessFixture(b)
			query := "needle"
			if operation == "fallback" {
				query = "unmatchedquery"
			}
			args, _ := json.Marshal(map[string]any{"query": query, "limit": 5})
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if operation == "no-user-facts" {
					eng.saveWebUserFacts("hello")
					continue
				}
				result, err := reg.Execute(context.Background(), "recall", args)
				if err != nil || result.Err != nil || !strings.Contains(result.Text, "portable") {
					b.Fatalf("recall: %v / %+v", err, result)
				}
			}
		})
	}
}

func TestWebMemoryToolsReuseEngineStores(t *testing.T) {
	eng, _, stores := webMemoryAccessFixture(t)
	for i, global := range []bool{true, false} {
		t.Run(fmt.Sprintf("global=%v", global), func(t *testing.T) {
			keeper := webMemoryKeeper{engine: eng, home: eng.Home(), global: global}
			got, err := keeper.open()
			if err != nil {
				t.Fatal(err)
			}
			if got != stores[i] {
				_ = got.Close()
				t.Fatal("tool reopened a memory store already owned by the engine")
			}
			again, err := keeper.open()
			if err != nil {
				t.Fatal(err)
			}
			if again != got {
				_ = again.Close()
				t.Fatal("second tool operation opened another store")
			}
		})
	}
}

func TestWebMemoryStoreConcurrentReuseAndClose(t *testing.T) {
	eng, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	const count = 12
	type opened struct {
		store  *memory.Store
		global bool
		err    error
	}
	results := make(chan opened, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			global := i%2 == 0
			keeper := webMemoryKeeper{engine: eng, home: eng.Home(), global: global}
			store, err := keeper.open()
			if err == nil {
				err = keeper.Put(memory.Entry{ID: fmt.Sprintf("parallel-%d", i), Scope: memory.ScopeFact, Content: fmt.Sprintf("worker %d durable note", i)})
			}
			results <- opened{store: store, global: global, err: err}
		}(i)
	}
	wg.Wait()
	close(results)
	stores := map[bool]*memory.Store{}
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if previous := stores[result.global]; previous != nil && previous != result.store {
			t.Fatal("concurrent calls opened multiple stores")
		}
		stores[result.global] = result.store
	}
	if stores[true] == stores[false] {
		t.Fatal("global and project stores must be distinct")
	}
	for _, store := range stores {
		entries, err := store.List(memory.ScopeFact, 0)
		if err != nil || len(entries) != count/2 {
			t.Fatalf("concurrent writes: count=%d err=%v", len(entries), err)
		}
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	for global, store := range stores {
		if _, err := store.Search("durable", 5); err == nil {
			t.Fatal("engine close left memory database open")
		}
		keeper := webMemoryKeeper{engine: eng, home: eng.Home(), global: global}
		if _, err := keeper.RecallSearch(context.Background(), "durable", 5); err == nil {
			t.Fatal("closed engine reopened memory")
		}
		if err := keeper.Put(memory.Entry{ID: "late", Scope: memory.ScopeFact, Content: "late write"}); err == nil {
			t.Fatal("write after shutdown succeeded")
		}
	}
}

func TestWebMemoryCacheSeesExternalWritesAndKeepsWorkspace(t *testing.T) {
	firstHome, secondHome := t.TempDir(), t.TempDir()
	eng, err := NewEngine(echoConfig(), firstHome, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	first := webMemoryKeeper{engine: eng, home: firstHome}
	if err := first.Put(memory.Entry{ID: "first", Scope: memory.ScopeFact, Content: "firstworkspace"}); err != nil {
		t.Fatal(err)
	}
	// A separate Store models a GUI memory edit or a CLI process writing SQLite.
	external, err := memory.OpenProjectStore(eng.DataDir(), firstHome)
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	if err := external.Put(memory.Entry{ID: "external", Scope: memory.ScopeFact, Content: "externalupdate"}); err != nil {
		t.Fatal(err)
	}
	hits, err := first.RecallSearch(context.Background(), "externalupdate", 5)
	if err != nil || len(hits) != 1 {
		t.Fatalf("cached connection missed committed external write: %v / %v", hits, err)
	}
	if err := external.Delete("external"); err != nil {
		t.Fatal(err)
	}
	hits, err = first.RecallSearch(context.Background(), "externalupdate", 5)
	if err != nil || len(hits) != 0 {
		t.Fatalf("cached connection retained deleted data: %v / %v", hits, err)
	}
	eng.setHome(secondHome)
	second := webMemoryKeeper{engine: eng, home: secondHome}
	if err := second.Put(memory.Entry{ID: "second", Scope: memory.ScopeFact, Content: "secondworkspace"}); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		keeper     webMemoryKeeper
		own, other string
	}{{first, "firstworkspace", "secondworkspace"}, {second, "secondworkspace", "firstworkspace"}} {
		own, err := check.keeper.Search(check.own, 5)
		if err != nil || len(own) != 1 {
			t.Fatalf("keeper lost its captured workspace: %v / %v", own, err)
		}
		other, err := check.keeper.Search(check.other, 5)
		if err != nil || len(other) != 0 {
			t.Fatalf("project data leaked across keepers: %v / %v", other, err)
		}
	}
	eng.saveWebUserFacts("My name is Anna")
	global := webMemoryKeeper{engine: eng, global: true}
	hits, err = global.Search("Anna", 5)
	if err != nil || len(hits) != 1 {
		t.Fatalf("deterministic facts were not saved: %v / %v", hits, err)
	}
	// An independent reader sees the write on disk, not only through a shared Go object.
	reader, err := memory.OpenStore(eng.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	hits, err = reader.Search("Anna", 5)
	if err != nil || len(hits) != 1 {
		t.Fatalf("deterministic fact not persisted: %v / %v", hits, err)
	}
}

func TestWebMemoryStoreRetriesFailedOpen(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(dataDir, []byte("blocks directory creation"), 0600); err != nil {
		t.Fatal(err)
	}
	eng := &Engine{dataDir: dataDir}
	defer eng.Close()
	keeper := webMemoryKeeper{engine: eng, global: true}
	if _, err := keeper.open(); err == nil {
		t.Fatal("expected first open failure")
	}
	if eng.globalMemory != nil {
		t.Fatal("failed open cached a store")
	}
	if err := os.Remove(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := keeper.open()
	if err != nil || store == nil {
		t.Fatalf("repaired directory was not retried: %v", err)
	}
	again, err := keeper.open()
	if err != nil || again != store {
		t.Fatalf("successful retry was not reused: %v", err)
	}
}
