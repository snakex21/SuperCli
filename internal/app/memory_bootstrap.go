package app

import (
	"log"

	"supercli/internal/storage/memory"
	"supercli/internal/system/config"
	"supercli/internal/tools"
)

// memoryBundle is the dual-store memory stack opened at startup.
// Callers must defer Close on Global and Project when non-nil.
type memoryBundle struct {
	Global    *memory.Store
	Project   *memory.Store
	AutoSaver *memory.AutoSaver
	Progress  *memProgress
	Briefing  string
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
	b.AutoSaver = &memory.AutoSaver{Project: memStore, Global: globalMemStore, ProjectPath: home}
	b.Progress = &memProgress{}
	return b
}

// registerMemoryTools wires remember/recall when either store is open.
func registerMemoryTools(registry *tools.Registry, b memoryBundle) {
	if b.Project == nil && b.Global == nil {
		return
	}
	// Persistent memory tools: always-on so the model can save
	// and recall facts across sessions. remember routes entries
	// to the project or global store via its `scope` argument;
	// recall searches both hybridly (FTS5 + vectors when an
	// embedder was detected).
	rememberTool := tools.NewRememberDual(storeOrNil(b.Project), storeOrNil(b.Global))
	rememberTool.OnSave = b.AutoSaver.NoteRemember
	registry.MustRegister(rememberTool.Spec())
	registry.MustRegister(tools.NewRecallDual(storeOrNil(b.Project), storeOrNil(b.Global)).Spec())
	registry.MarkAlwaysOn("remember")
	registry.MarkAlwaysOn("recall")
}

// applyAlwaysOnToolProfile marks the practical core tools for the
// execution profile. ThinTools decides which of these carry a full
// schema vs catalog entry; small_full_tools is the escape hatch.
func applyAlwaysOnToolProfile(registry *tools.Registry, coordinatorMode, thinTools bool) {
	if coordinatorMode {
		registry.MarkAlwaysOn("ask_user")
		registry.MarkAlwaysOn("tool_search")
		registry.MarkAlwaysOn("goal")
		return
	}
	if thinTools {
		// Prefer patch_file/create_file for mutations. Legacy line editors
		// remain registered for workers/compat but are not always-on core.
		for _, name := range []string{
			"read_lines", "read_many", "read_image", "search_code",
			"ctx_execute", "ask_user", "patch_file", "create_file",
			"list_dir",
			"goal", "tool_search", "invoke_tool",
			// read_image stays always-on for attachment/vision turns.
		} {
			registry.MarkAlwaysOn(name)
		}
		return
	}
	registry.MarkAlwaysOn("read_image")
	registry.MarkAlwaysOn("ask_user")
	registry.MarkAlwaysOn("tool_search")
	registry.MarkAlwaysOn("invoke_tool")
	registry.MarkAlwaysOn("apply_skill")
	// darwin is deliberately NOT always-on: its schema (~277 tok)
	// is a heavy always-on prefix that overlaps conceptually with
	// task/orchestrator delegation. It stays reachable on demand
	// via tool_search + the /darwin slash command.
	registry.MarkAlwaysOn("goal")
	registry.MarkAlwaysOn("ctx_execute")
}
