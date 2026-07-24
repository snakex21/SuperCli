package app

import (
	"context"
	"log"
	"path/filepath"

	"supercli/internal/agent/reflect"
	"supercli/internal/storage/memory"
)

// startPatternInjector builds the F5 pattern injector when project
// memory is available. Pattern extraction runs in the background so
// tool_errors.log parsing never blocks TUI startup.
func startPatternInjector(memStore *memory.Store, dataDir, logsDir string) *reflect.Injector {
	if memStore == nil {
		return nil
	}
	patStore, _ := reflect.NewStore(memStore)
	if patStore == nil {
		return nil
	}
	ext := &reflect.Extractor{
		ErrorsPath:  filepath.Join(logsDir, "tool_errors.log"),
		MaxPatterns: 5,
	}
	// Pattern extraction parses the whole tool_errors.log;
	// run it off the startup path (the injector reads the
	// store lazily, so late-stored patterns still apply).
	go func() {
		defer recoverAndLog(dataDir)()
		patterns, extErr := ext.Extract(context.Background())
		if extErr != nil {
			log.Printf("F5 extract: %v", extErr)
		} else if len(patterns) > 0 {
			if saveErr := patStore.SaveAll(context.Background(), patterns); saveErr != nil {
				log.Printf("F5 save: %v", saveErr)
			} else {
				log.Printf("F5: stored %d patterns", len(patterns))
			}
		}
	}()
	return &reflect.Injector{Store: patStore}
}
