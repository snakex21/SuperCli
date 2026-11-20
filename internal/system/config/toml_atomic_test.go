package config

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// A turn reads config.toml while a settings save rewrites it. The reader must
// see the old or the new file, never an empty or half-written one (a turn
// that started during a language change ran with an empty configuration).
func TestSaveTomlNeverExposesPartialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	on := true
	base := TomlConfig{Orchestrator: &on, Language: "pl"}
	if err := SaveToml(path, base); err != nil {
		t.Fatal(err)
	}
	var stop atomic.Bool
	var bad atomic.Int32
	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				cfg, err := LoadToml(path)
				if err != nil || cfg.Orchestrator == nil || !*cfg.Orchestrator || cfg.Language == "" {
					bad.Add(1)
				}
			}
		}()
	}
	for i := 0; i < 300; i++ {
		next := base
		if i%2 == 0 {
			next.Language = "en"
		}
		if err := SaveToml(path, next); err != nil {
			stop.Store(true)
			wg.Wait()
			t.Fatalf("save %d: %v", i, err)
		}
	}
	stop.Store(true)
	wg.Wait()
	if n := bad.Load(); n > 0 {
		t.Fatalf("readers saw a missing, empty or partial config.toml %d time(s)", n)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}
