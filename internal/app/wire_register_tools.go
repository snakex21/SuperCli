package app

import (
	"log"

	"supercli/internal/assembly"
	"supercli/internal/storage/goal"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
	"supercli/internal/tools"
)

// interactiveToolset is the TUI adapter over the shared toolset table. The
// interactive registry fills in phases (assembly.TUIEarly, TUICore, the
// post-loop delegation/MCP rows and TUILate) because several tools bind to
// objects created in between (the loop, the session store, the MCP manager).
// Callers own Close on CodeIntel and ProcessSession and must call FTSClose.
type interactiveToolset struct {
	Registry       *tools.Registry
	AskCh          chan tools.AskRequest
	CodeIntel      *tools.CodeIntel
	ProcessSession *tools.ProcessSession
	ToolSearcher   *tools.ToolSearcher
	FTSClose       func()
	deps           *assembly.Deps
}

// newInteractiveToolset registers the early TUI rows (media, office, search,
// skills, user tools). checkpoint may be nil (no undo snapshots).
func newInteractiveToolset(registry *tools.Registry, home, dataDir string, tomlCfg config.TomlConfig, checkpoint func(tools.Tool) tools.Tool) *interactiveToolset {
	ftsIndex, err := tools.NewInMemoryIndex()
	if err != nil {
		fatal("init fts index", err)
	}
	ts := &interactiveToolset{
		Registry:       registry,
		AskCh:          make(chan tools.AskRequest, 4),
		CodeIntel:      tools.NewCodeIntel(home),
		ProcessSession: tools.NewProcessSession(home, dataDir),
		FTSClose:       func() { _ = ftsIndex.Close() },
	}
	ts.ToolSearcher = tools.NewToolSearcher(registry, ftsIndex)
	ts.deps = &assembly.Deps{
		Home:           home,
		DataDir:        dataDir,
		Toml:           tomlCfg,
		Registry:       registry,
		Checkpoint:     checkpoint,
		CodeIntel:      ts.CodeIntel,
		ProcessSession: ts.ProcessSession,
		ToolSearcher:   ts.ToolSearcher,
		AskCh:          ts.AskCh,
	}
	assembly.Register(registry, assembly.TUIEarly, ts.deps)
	return ts
}

// registerCore adds the rows that follow the always-on profile and memory
// stack: remember/recall (when a store is open), ctx_execute, goal (with
// decomposition on the main provider), host power, darwin (nil = disabled)
// and search_history (when the session store opened).
func (ts *interactiveToolset) registerCore(mem memoryBundle, goals *goal.Service, goalSetup func(*tools.GoalTool), darwin *tools.Tool, sessStore *session.Store) {
	provided := map[string]tools.Tool{}
	if mem.Project != nil || mem.Global != nil {
		// remember routes entries to the project or global store via its
		// `scope` argument; recall searches both (FTS5 + vectors when an
		// embedder was detected).
		rememberTool := tools.NewRememberDual(gateMemoryKeeper(mem.Project), gateMemoryKeeper(mem.Global))
		rememberTool.OnSave = mem.AutoSaver.NoteRemember
		provided["remember"] = rememberTool.Spec()
		// The gated keepers refuse while /memory off is active mid-session;
		// the tool list itself stays stable (see gatedKeeper).
		provided["recall"] = tools.NewRecallDual(gateMemoryKeeper(mem.Project), gateMemoryKeeper(mem.Global)).Spec()
	}
	if darwin != nil {
		provided["darwin"] = *darwin
	}
	if sessStore != nil {
		// Opt-in: the model discovers it via tool_search.
		provided["search_history"] = tools.NewSearchHistory(sessStore).Spec()
	}
	ts.deps.Goals = goals
	ts.deps.GoalSetup = goalSetup
	ts.deps.Provided = provided
	assembly.Register(ts.Registry, assembly.TUICore, ts.deps)
	ts.deps.Provided = nil
}

// registerLate adds the line-level file ops, web tools, library finder,
// invoke_tool and mail tools, then re-indexes tool_search: many tools were
// registered after the first index build and would otherwise be invisible
// to tool_search (and unreachable for small-tier models).
func (ts *interactiveToolset) registerLate() {
	assembly.Register(ts.Registry, assembly.TUILate, ts.deps)
	if err := ts.ToolSearcher.RebuildIndex(); err != nil {
		log.Printf("tool_search reindex: %v", err)
	}
}

// applyAlwaysOnToolProfile marks the TUI's execution-profile core
// (assembly.AlwaysOnProfile).
func applyAlwaysOnToolProfile(registry *tools.Registry, coordinatorMode, thinTools bool) {
	for _, name := range assembly.AlwaysOnProfile(coordinatorMode, thinTools) {
		registry.MarkAlwaysOn(name)
	}
}
