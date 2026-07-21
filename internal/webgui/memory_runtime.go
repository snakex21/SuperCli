package webgui

import (
	"context"

	"supercli/internal/storage/memory"
)

// webMemoryKeeper opens the appropriate SQLite memory store for one operation.
// Web runs are short-lived and created per request, so keeping a raw *Store on
// the tool would either leak handles or close it before the model can call it.
type webMemoryKeeper struct {
	dataDir string
	home    string
	global  bool
}

func (k webMemoryKeeper) open() (*memory.Store, error) {
	if k.global {
		return memory.OpenStore(k.dataDir)
	}
	return memory.OpenProjectStore(k.dataDir, k.home)
}

func (k webMemoryKeeper) Put(entry memory.Entry) error {
	store, err := k.open()
	if err != nil {
		return err
	}
	defer store.Close()
	return store.Put(entry)
}

func (k webMemoryKeeper) Search(query string, limit int) ([]memory.Entry, error) {
	store, err := k.open()
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.Search(query, limit)
}

func (k webMemoryKeeper) Recent(scope string, limit int) ([]memory.Entry, error) {
	store, err := k.open()
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.Recent(scope, limit)
}

func (k webMemoryKeeper) HybridSearch(ctx context.Context, query string, limit int) ([]memory.Entry, error) {
	store, err := k.open()
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.HybridSearch(ctx, query, limit)
}

func webMemoryBriefing(dataDir, home string, tokenCap int) string {
	if tokenCap <= 0 {
		tokenCap = 700
	}
	globalStore, globalErr := memory.OpenStore(dataDir)
	if globalErr == nil {
		defer globalStore.Close()
	}
	projectStore, projectErr := memory.OpenProjectStore(dataDir, home)
	if projectErr == nil {
		defer projectStore.Close()
	}
	if globalErr != nil && projectErr != nil {
		return ""
	}
	return memory.BuildBriefing(globalStore, projectStore, home, tokenCap)
}

func saveWebUserFacts(dataDir, prompt string) {
	globalStore, err := memory.OpenStore(dataDir)
	if err != nil {
		return
	}
	defer globalStore.Close()
	saver := &memory.AutoSaver{Global: globalStore}
	saver.SaveDeterministicUserFacts([]string{prompt})
}
