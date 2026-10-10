package webgui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"supercli/internal/storage/memory"
)

func webMemoryIdentityFixture(t *testing.T) (*Engine, string) {
	t.Helper()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	eng := &Engine{dataDir: dataDir}
	t.Cleanup(func() {
		if err := eng.Close(); err != nil {
			t.Error(err)
		}
	})
	return eng, root
}

func TestWebMemoryRelocationSharesStoreAndKeepsWorkerBindings(t *testing.T) {
	eng, root := webMemoryIdentityFixture(t)
	home := filepath.Join(root, "workspace-before")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	eng.home = home
	if err := eng.projectAction("add", home, "portable fixture", ""); err != nil {
		t.Fatal(err)
	}
	firstKeeper := webMemoryKeeper{engine: eng, home: home}
	first, err := firstKeeper.open()
	if err != nil {
		t.Fatal(err)
	}
	keepers := []webMemoryKeeper{firstKeeper}
	for index := 0; index < 3; index++ {
		next := filepath.Join(root, fmt.Sprintf("workspace-after-%d", index))
		if err := os.MkdirAll(next, 0700); err != nil {
			t.Fatal(err)
		}
		if err := eng.projectAction("relocate", home, "", next); err != nil {
			t.Fatal(err)
		}
		keeper := webMemoryKeeper{engine: eng, home: next}
		store, err := keeper.open()
		if err != nil || store != first {
			t.Fatalf("relocated keeper opened another Store: %v", err)
		}
		keepers = append(keepers, keeper)
		home = next
	}
	// Earlier homes no longer have a registered storage key, but running tools
	// with those captured homes must keep using the original database.
	for index, keeper := range keepers {
		if err := keeper.Put(memory.Entry{ID: fmt.Sprintf("worker-%d", index), Scope: memory.ScopeFact, Content: fmt.Sprintf("sharedworker note %d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, keeper := range keepers {
		entries, err := keeper.Search("sharedworker", 10)
		if err != nil || len(entries) != len(keepers) {
			t.Fatalf("worker changed its bound project: count=%d err=%v", len(entries), err)
		}
	}
	if len(eng.projectMemory) != len(keepers) || len(eng.projectMemoryByDB) != 1 {
		t.Fatalf("homes=%d databases=%d", len(eng.projectMemory), len(eng.projectMemoryByDB))
	}
	projects := memory.LoadProjectsMap(eng.dataDir)
	if len(projects) != 1 || projects[home] == "" {
		t.Fatal("old keepers recreated removed project mappings")
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	if eng.projectMemory != nil || eng.projectMemoryByDB != nil {
		t.Fatal("Close retained memory indexes")
	}
	if _, err := first.Search("sharedworker", 1); err == nil {
		t.Fatal("Close left shared SQLite open")
	}
	if _, err := firstKeeper.open(); err == nil {
		t.Fatal("old keeper reopened a closed Engine")
	}
}

func TestWebMemoryRemovedRelocatedProjectKeepsObservedStore(t *testing.T) {
	eng, root := webMemoryIdentityFixture(t)
	oldHome, newHome := filepath.Join(root, "original"), filepath.Join(root, "relocated")
	for _, home := range []string{oldHome, newHome} {
		if err := os.MkdirAll(home, 0700); err != nil {
			t.Fatal(err)
		}
	}
	eng.home = oldHome
	if err := eng.projectAction("add", oldHome, "fixture", ""); err != nil {
		t.Fatal(err)
	}
	oldKeeper := webMemoryKeeper{engine: eng, home: oldHome}
	first, err := oldKeeper.open()
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.projectAction("relocate", oldHome, "", newHome); err != nil {
		t.Fatal(err)
	}
	newKeeper := webMemoryKeeper{engine: eng, home: newHome}
	if store, err := newKeeper.open(); err != nil || store != first {
		t.Fatalf("relocation did not share Store: %v", err)
	}
	if err := eng.projectAction("remove", newHome, "", ""); err != nil {
		t.Fatal(err)
	}
	for index, keeper := range []webMemoryKeeper{oldKeeper, newKeeper} {
		if err := keeper.Put(memory.Entry{ID: fmt.Sprintf("removed-%d", index), Scope: memory.ScopeFact, Content: "removedworker durable note"}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := first.Search("removedworker", 10)
	if err != nil || len(entries) != 2 {
		t.Fatalf("removal stranded existing workers: count=%d err=%v", len(entries), err)
	}
	if len(memory.LoadProjectsMap(eng.dataDir)) != 0 || len(eng.listProjects()) != 0 {
		t.Fatal("existing worker access recreated the removed project")
	}
}

func TestWebMemoryUnregisteredAndWindowsAliasesKeepStorageIdentity(t *testing.T) {
	eng, root := webMemoryIdentityFixture(t)
	home := filepath.Join(root, "unregistered")
	first, err := eng.webMemoryStore(home, false)
	if err != nil {
		t.Fatal(err)
	}
	if store, err := eng.webMemoryStore(home+string(os.PathSeparator)+".", false); err != nil || store != first {
		t.Fatalf("clean home alias did not reuse Store: %v", err)
	}
	if memory.LoadProjectsMap(eng.dataDir)[home] != memory.ProjectKey(home) {
		t.Fatal("unregistered project lost its durable storage mapping")
	}
	if len(memory.LoadWorkspace(eng.dataDir).Projects) != 0 {
		t.Fatal("opening memory unexpectedly added a named workspace")
	}
	if runtime.GOOS != "windows" {
		return
	}
	// A case alias must preserve an observed authoritative key, rather than
	// computing another fallback key because JSON keys are case-sensitive.
	other := filepath.Join(root, "case-workspace")
	projects := memory.LoadProjectsMap(eng.dataDir)
	projects[other] = "relocated-authoritative-key"
	if err := memory.SaveProjectsMap(eng.dataDir, projects); err != nil {
		t.Fatal(err)
	}
	authoritative, err := eng.webMemoryStore(other, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{strings.ToUpper(other), strings.ToLower(other)} {
		store, err := eng.webMemoryStore(alias, false)
		if err != nil || store != authoritative {
			t.Fatalf("Windows case alias lost authoritative binding: %v", err)
		}
	}
	if len(eng.projectMemoryByDB) != 2 {
		t.Fatal("Windows aliases opened additional backing databases")
	}
	if len(memory.LoadProjectsMap(eng.dataDir)) != len(projects) {
		t.Fatal("case spelling lookup rewrote project registration")
	}
}

func TestWebMemoryMappedAliasesConcurrentReuse(t *testing.T) {
	eng, root := webMemoryIdentityFixture(t)
	const count = 12
	projects := make(map[string]string, count)
	homes := make([]string, count)
	for index := range homes {
		homes[index] = filepath.Join(root, fmt.Sprintf("workspace-%d", index))
		projects[homes[index]] = "shared-portable-project"
	}
	if err := memory.SaveProjectsMap(eng.dataDir, projects); err != nil {
		t.Fatal(err)
	}
	type result struct {
		store *memory.Store
		err   error
	}
	results := make(chan result, count)
	var work sync.WaitGroup
	for index, home := range homes {
		work.Add(1)
		go func(index int, home string) {
			defer work.Done()
			keeper := webMemoryKeeper{engine: eng, home: home}
			store, err := keeper.open()
			if err == nil {
				err = keeper.Put(memory.Entry{ID: fmt.Sprintf("parallel-%d", index), Scope: memory.ScopeFact, Content: "parallelidentity durable note"})
			}
			results <- result{store, err}
		}(index, home)
	}
	work.Wait()
	close(results)
	var first *memory.Store
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if first != nil && first != result.store {
			t.Fatal("parallel mapped homes opened different Stores")
		}
		first = result.store
	}
	entries, err := first.Search("parallelidentity", count+1)
	if err != nil || len(entries) != count {
		t.Fatalf("parallel alias writes: count=%d err=%v", len(entries), err)
	}
	if len(eng.projectMemoryByDB) != 1 || len(eng.projectMemory) != count {
		t.Fatalf("databases=%d homes=%d", len(eng.projectMemoryByDB), len(eng.projectMemory))
	}
	for key, store := range eng.projectMemoryByDB {
		if key != webMemoryDatabaseKey(store.Root()) {
			t.Fatal("database index used metadata prediction instead of opened Root")
		}
	}
	if got := memory.LoadProjectsMap(eng.dataDir); len(got) != count {
		t.Fatal("sharing aliases lost registered project metadata")
	}
}

func TestWebMemoryObservedBindingsDoNotOverwriteProjectEdits(t *testing.T) {
	eng, root := webMemoryIdentityFixture(t)
	firstHome, secondHome := filepath.Join(root, "first-home"), filepath.Join(root, "second-home")
	projects := map[string]string{firstHome: "original-storage", secondHome: "original-storage"}
	if err := memory.SaveProjectsMap(eng.dataDir, projects); err != nil {
		t.Fatal(err)
	}
	first, err := eng.webMemoryStore(firstHome, false)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := eng.webMemoryStore(secondHome, false); err != nil || second != first {
		t.Fatalf("initial aliases not shared: %v", err)
	}
	projects[firstHome] = "externally-edited-storage"
	projects[filepath.Join(root, "external-project")] = "preserved-metadata"
	failures := make(chan error, 2)
	var work sync.WaitGroup
	work.Add(2)
	go func() {
		defer work.Done()
		for index := 0; index < 30; index++ {
			if err := memory.SaveProjectsMap(eng.dataDir, projects); err != nil {
				failures <- err
				return
			}
		}
	}()
	go func() {
		defer work.Done()
		for index := 0; index < 200; index++ {
			for _, home := range []string{firstHome, secondHome} {
				store, err := eng.webMemoryStore(home, false)
				if err != nil || store != first {
					failures <- fmt.Errorf("metadata edit changed observed worker binding: %v", err)
					return
				}
			}
		}
	}()
	work.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if got := memory.LoadProjectsMap(eng.dataDir); got[firstHome] != projects[firstHome] || got[filepath.Join(root, "external-project")] != "preserved-metadata" {
		t.Fatal("cached worker lookups overwrote durable metadata edits")
	}
	// A fresh Engine observes the new authoritative mapping; only keepers whose
	// home was already bound keep the prior Store.
	fresh := &Engine{dataDir: eng.dataDir}
	t.Cleanup(func() { _ = fresh.Close() })
	updated, err := fresh.webMemoryStore(firstHome, false)
	if err != nil || webMemoryDatabaseKey(updated.Root()) == webMemoryDatabaseKey(first.Root()) {
		t.Fatalf("fresh Engine ignored updated project mapping: %v", err)
	}
}

func TestWebMemoryDistinctBackingDatabasesStayIsolated(t *testing.T) {
	eng, root := webMemoryIdentityFixture(t)
	first, err := eng.webMemoryStore(filepath.Join(root, "first"), false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := eng.webMemoryStore(filepath.Join(root, "second"), false)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || len(eng.projectMemoryByDB) != 2 {
		t.Fatal("distinct database identities were merged")
	}
	if err := first.Put(memory.Entry{ID: "isolated", Scope: memory.ScopeFact, Content: "firstidentity note"}); err != nil {
		t.Fatal(err)
	}
	if entries, err := second.Search("firstidentity", 5); err != nil || len(entries) != 0 {
		t.Fatalf("memory leaked between distinct databases: count=%d err=%v", len(entries), err)
	}
}

func TestWebMemorySymlinkedBackingDatabaseSharesStore(t *testing.T) {
	eng, root := webMemoryIdentityFixture(t)
	firstHome, aliasHome := filepath.Join(root, "first-home"), filepath.Join(root, "alias-home")
	first, err := eng.webMemoryStore(firstHome, false)
	if err != nil {
		t.Fatal(err)
	}
	aliasKey := "backing-directory-alias"
	if err := os.Symlink(first.Root(), filepath.Join(eng.dataDir, "projects", aliasKey)); err != nil {
		t.Skipf("backing directory symlinks unavailable: %v", err)
	}
	projects := memory.LoadProjectsMap(eng.dataDir)
	projects[aliasHome] = aliasKey
	if err := memory.SaveProjectsMap(eng.dataDir, projects); err != nil {
		t.Fatal(err)
	}
	alias, err := eng.webMemoryStore(aliasHome, false)
	if err != nil || alias != first || len(eng.projectMemoryByDB) != 1 {
		t.Fatalf("physical backing directory alias opened another Store: %v", err)
	}
}
