package app

import (
	"log"

	"supercli/internal/storage/memory"
	"supercli/internal/system/config"
)

// memoryBundle is the dual-store memory stack opened at startup.
// Callers must defer Close on Global and Project when non-nil.
type memoryBundle struct {
	Global         *memory.Store
	Project        *memory.Store
	AutoSaver      *memory.AutoSaver
	Progress       *memProgress
	Briefing       string
	BriefingForRun func() string
}

// openMemoryStack opens global + project SQLite stores, starts
// background embedder detection, refreshes the project card and
// builds the session-start briefing. Failures are non-fatal.
func openMemoryStack(dataDir, home, apiKey string, smallTier bool, tomlCfg config.TomlConfig) memoryBundle {
	var b memoryBundle
	globalMemStore, gMemErr := memory.OpenStore(dataDir)
	if gMemErr != nil {
		log.Printf("global memory store: %v (global memory disabled)", gMemErr)
		globalMemStore = nil
	}
	memStore, memErr := memory.OpenProjectStore(dataDir, home)
	if memErr != nil {
		log.Printf("project memory store: %v (F5 disabled)", memErr)
		memStore = nil
	}
	b.Global = globalMemStore
	b.Project = memStore

	// Embedding backend detection pings local servers, so it runs
	// in the background; until it lands, searches are FTS5-only.
	go func() {
		defer recoverAndLog(dataDir)()
		if e := memory.DetectEmbedder(apiKey); e != nil {
			if globalMemStore != nil {
				globalMemStore.SetEmbedder(e)
			}
			if memStore != nil {
				memStore.SetEmbedder(e)
			}
		}
	}()

	// Refresh this project's card (bumps last-session) and build
	// the session-start briefing injected into the system prompt.
	memory.RefreshCard(globalMemStore, home, "", "active")
	briefCap := 700
	if smallTier {
		briefCap = 300
	}
	// A configured memory_briefing_tokens is a hard override of the
	// tier default (the briefing packs preferences + freshest journal
	// lines up to this cap; anything over stays in the DB for recall).
	if tomlCfg.MemoryBriefingTokens > 0 {
		briefCap = tomlCfg.MemoryBriefingTokens
	}
	b.Briefing = memory.BuildBriefing(globalMemStore, memStore, home, briefCap)
	b.BriefingForRun = func() string {
		return memory.BuildBriefing(globalMemStore, memStore, home, briefCap)
	}
	b.AutoSaver = &memory.AutoSaver{Project: memStore, Global: globalMemStore, ProjectPath: home}
	b.Progress = &memProgress{}
	return b
}
