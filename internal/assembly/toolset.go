// Package assembly is the shared runtime composition of SuperCli: one
// declarative toolset table, one system-prompt assembler, one approval-gate
// constructor and one LoopConfig builder used by every surface (interactive
// TUI, --batch and the web GUI, including the NestCafe app profile).
//
// Surfaces stay thin adapters: they own their long-lived state (stores,
// checkpoint controllers, provider factories) and hand it in as data. Every
// deliberate difference between surfaces is a visible field or table row here,
// with a comment saying why it differs.
package assembly

import (
	"log"
	"time"

	"supercli/internal/agent"
	"supercli/internal/storage/goal"
	"supercli/internal/storage/library"
	"supercli/internal/system/config"
	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
	toolhostpower "supercli/internal/tools/hostpower"
	"supercli/internal/tools/mcp"
	"supercli/internal/tools/mediagen"
)

// Surface names a front-end composition root.
type Surface string

const (
	SurfaceTUI   Surface = "tui"
	SurfaceBatch Surface = "batch"
	SurfaceWeb   Surface = "web"
)

// Wrap selects the wrappers applied to a tool, always in this order:
// checkpoint undo snapshot first, then code-intel invalidation outermost.
type Wrap uint8

const (
	// WrapCheckpoint applies Deps.Checkpoint (undo snapshots) when set.
	WrapCheckpoint Wrap = 1 << iota
	// WrapCodeIntel applies Deps.CodeIntel.WrapMutation when set.
	WrapCodeIntel
	// WrapMutation is the full mutation wrapper: undo + code-intel invalidation.
	WrapMutation = WrapCheckpoint | WrapCodeIntel
)

// Entry is one row of a surface toolset: which tool, which wrappers, and
// whether it is always-on (full schema or core catalog entry every turn).
type Entry struct {
	Tool     string
	Wrap     Wrap
	AlwaysOn bool
}

// Deps carries everything tool constructors need. Fields left zero simply
// skip the tools that depend on them (an optional tool is never an error).
type Deps struct {
	Home, DataDir string
	Toml          config.TomlConfig
	// Registry is the registry being filled; invoke_tool and the lexical
	// tool_search close over it.
	Registry *tools.Registry

	Checkpoint func(tools.Tool) tools.Tool
	CodeIntel  *tools.CodeIntel
	// CodeIntelWrap overrides CodeIntel.WrapMutation as the code-intel
	// invalidation wrapper (tests install a spy here).
	CodeIntelWrap  func(tools.Tool) tools.Tool
	ProcessSession *tools.ProcessSession
	// ToolSearcher is the surface-owned tool_search (the TUI keeps an FTS
	// index it rebuilds later). Nil builds a lexical searcher over Registry.
	ToolSearcher *tools.ToolSearcher
	// Skills is the skill discoverer; nil builds one for Home/DataDir.
	Skills *tools.Discoverer
	// AskCh is the interactive question channel. ask_user and host_power
	// exist only on surfaces with a human to answer.
	AskCh      chan<- tools.AskRequest
	AskTimeout time.Duration
	// Browser is a shared browser tool; nil builds a per-registry browser.
	Browser *tools.Browser
	// OfficeOnly restricts ctx_execute to native office tooling (NestCafe).
	OfficeOnly bool
	// Goals backs the goal tool; GoalSetup customises it (decomposition).
	Goals     *goal.Service
	GoalSetup func(*tools.GoalTool)
	// MCP is the lazily started MCP manager behind mcp_bridge.
	MCP *mcp.Manager
	// Provided holds prebuilt specs for tools whose state the surface owns
	// (memory stores, session store, darwin, delegation, consult).
	Provided map[string]tools.Tool

	webSearch *tools.WebSearch
}

func (d *Deps) web() *tools.WebSearch {
	if d.webSearch == nil {
		d.webSearch = tools.NewConfiguredWebSearch(d.Toml)
	}
	return d.webSearch
}

// builder returns the specs for one table row plus extra always-on names it
// decides at runtime (media generation advertises configured tools).
type builder func(d *Deps) (specs []tools.Tool, alwaysOn []string)

func one(f func(d *Deps) tools.Tool) builder {
	return func(d *Deps) ([]tools.Tool, []string) { return []tools.Tool{f(d)}, nil }
}

func provided(name string) builder {
	return func(d *Deps) ([]tools.Tool, []string) {
		if spec, ok := d.Provided[name]; ok {
			return []tools.Tool{spec}, nil
		}
		return nil, nil
	}
}

// catalog is the single constructor per tool id.
var catalog = map[string]builder{
	"read_image":      one(func(d *Deps) tools.Tool { return tools.NewReadImage(d.Home, 0).Spec() }),
	"search_code":     one(func(d *Deps) tools.Tool { return tools.NewSearchCode(d.Home).Spec() }),
	"read_zip":        one(func(d *Deps) tools.Tool { return tools.NewReadZip(d.Home, 0).Spec() }),
	"read_docx":       one(func(d *Deps) tools.Tool { return tools.NewReadDocx(d.Home, 0).Spec() }),
	"read_xlsx":       one(func(d *Deps) tools.Tool { return tools.NewReadXlsx(d.Home, 0).Spec() }),
	"read_pdf":        one(func(d *Deps) tools.Tool { return tools.NewReadPdf(d.Home, 0).Spec() }),
	"edit_docx":       one(func(d *Deps) tools.Tool { return tools.NewEditDocx(d.Home).Spec() }),
	"edit_xlsx":       one(func(d *Deps) tools.Tool { return tools.NewEditXlsx(d.Home).Spec() }),
	"list_dir":        one(func(d *Deps) tools.Tool { return tools.NewListDir(d.Home).Spec() }),
	"read_lines":      one(func(d *Deps) tools.Tool { return tools.NewReadLines(d.Home).Spec() }),
	"read_context":    one(func(d *Deps) tools.Tool { return tools.NewReadContext(d.Home).Spec() }),
	"read_many":       one(func(d *Deps) tools.Tool { return tools.NewReadMany(d.Home).Spec() }),
	"scratchpad":      one(func(d *Deps) tools.Tool { return tools.NewScratchpad(d.Home).Spec() }),
	"patch_file":      one(func(d *Deps) tools.Tool { return tools.NewPatchFile(d.Home).Spec() }),
	"create_file":     one(func(d *Deps) tools.Tool { return tools.NewCreateFile(d.Home).Spec() }),
	"edit_files":      one(func(d *Deps) tools.Tool { return tools.NewEditFiles(d.Home).Spec() }),
	"write_file":      one(func(d *Deps) tools.Tool { return tools.NewWriteFile(d.Home).Spec() }),
	"make_dir":        one(func(d *Deps) tools.Tool { return tools.NewMakeDir(d.Home).Spec() }),
	"move":            one(func(d *Deps) tools.Tool { return tools.NewMove(d.Home).Spec() }),
	"copy":            one(func(d *Deps) tools.Tool { return tools.NewCopy(d.Home).Spec() }),
	"trash":           one(func(d *Deps) tools.Tool { return tools.NewTrash(d.Home).Spec() }),
	"show_media":      one(func(d *Deps) tools.Tool { return tools.NewShowMedia(d.Home).Spec() }),
	"send_screenshot": one(func(d *Deps) tools.Tool { return tools.NewSendScreenshot(d.DataDir, nil).Spec() }),
	"headless_control": one(func(d *Deps) tools.Tool {
		return tools.NewHeadlessControl(d.Home, d.DataDir).Spec()
	}),
	"web_fetch":        one(func(d *Deps) tools.Tool { return tools.NewWebFetch().Spec() }),
	"web_download":     one(func(d *Deps) tools.Tool { return tools.NewWebDownload(d.Home).Spec() }),
	"web_search":       one(func(d *Deps) tools.Tool { return d.web().Spec() }),
	"web_lookup":       one(func(d *Deps) tools.Tool { return d.web().LookupSpec() }),
	"outlook_mail":     one(func(d *Deps) tools.Tool { return tools.NewOutlookMail().Spec() }),
	"thunderbird_mail": one(func(d *Deps) tools.Tool { return tools.NewThunderbirdMail(d.DataDir).Spec() }),
	"invoke_tool":      one(func(d *Deps) tools.Tool { return agent.NewInvokeTool(d.Registry).Spec() }),
	"check_library_alternatives": one(func(d *Deps) tools.Tool {
		return tools.NewCheckLibraryAlternatives(library.NewFinder()).Spec()
	}),
	"browser": one(func(d *Deps) tools.Tool {
		if d.Browser != nil {
			return d.Browser.Spec()
		}
		return tools.NewBrowser().Spec()
	}),
	"ctx_execute": one(func(d *Deps) tools.Tool {
		ctxTool := tools.NewCtxExecuteTool(ctxexec.New(d.Home), d.Home)
		ctxTool.NativeOfficeOnly = d.OfficeOnly
		return ctxTool.Spec()
	}),
	"apply_skill": one(func(d *Deps) tools.Tool {
		discoverer := d.Skills
		if discoverer == nil {
			discoverer = tools.NewDiscovererWithBuiltins(d.Home, d.DataDir)
		}
		return tools.NewSkillApplier(discoverer).Spec()
	}),
	"tool_search": one(func(d *Deps) tools.Tool {
		if d.ToolSearcher == nil {
			// Short-lived registries need no FTS database: the lexical
			// fallback keeps the exact tool_search contract.
			d.ToolSearcher = tools.NewToolSearcher(d.Registry, nil)
		}
		return d.ToolSearcher.Spec()
	}),
	"code_intel": func(d *Deps) ([]tools.Tool, []string) {
		if d.CodeIntel == nil {
			return nil, nil
		}
		return []tools.Tool{d.CodeIntel.Spec()}, nil
	},
	"process_session": func(d *Deps) ([]tools.Tool, []string) {
		if d.ProcessSession == nil {
			return nil, nil
		}
		return []tools.Tool{d.ProcessSession.Spec()}, nil
	},
	"ask_user": func(d *Deps) ([]tools.Tool, []string) {
		if d.AskCh == nil {
			return nil, nil
		}
		ask := tools.NewAskUser(d.AskCh)
		if d.AskTimeout > 0 {
			ask.Timeout = d.AskTimeout
		}
		return []tools.Tool{ask.Spec()}, nil
	},
	"host_power": func(d *Deps) ([]tools.Tool, []string) {
		// Host power needs a human to confirm; non-interactive registries
		// (batch, context/compaction helper loops) never get it.
		if d.AskCh == nil {
			return nil, nil
		}
		return []tools.Tool{toolhostpower.Spec()}, nil
	},
	"goal": func(d *Deps) ([]tools.Tool, []string) {
		if d.Goals == nil {
			return nil, nil
		}
		gt := tools.NewGoalTool(d.Goals)
		if d.GoalSetup != nil {
			d.GoalSetup(gt)
		}
		return []tools.Tool{gt.Spec()}, nil
	},
	"mcp_bridge": func(d *Deps) ([]tools.Tool, []string) {
		if d.MCP == nil || len(d.MCP.Names()) == 0 {
			return nil, nil
		}
		return []tools.Tool{mcp.NewBridge(d.MCP).Spec()}, nil
	},
	"media_generation": func(d *Deps) ([]tools.Tool, []string) {
		cfg, err := config.LoadMediaGeneration(d.DataDir)
		if err != nil {
			log.Printf("media generation configuration: %v", err)
			return nil, nil
		}
		dataDir := d.DataDir
		keys := func(name string) (string, string, bool) { return config.MediaGenerationProviderKey(dataDir, name) }
		return []tools.Tool{
			mediagen.NewImage(d.Home, cfg.Image).WithProviderKeys(keys).Spec(),
			mediagen.NewVideo(d.Home, cfg.Video).WithProviderKeys(keys).Spec(),
		}, mediagen.AdvertisedTools(cfg)
	},
	"user_tools": func(d *Deps) ([]tools.Tool, []string) {
		userTools, userErrs := tools.NewUserToolLoader(d.Home, d.DataDir).Load()
		for _, e := range userErrs {
			log.Printf("user tool load: %v", e)
		}
		return userTools, nil
	},
	// Tools whose state the surface owns.
	"remember":       provided("remember"),
	"recall":         provided("recall"),
	"search_history": provided("search_history"),
	"darwin":         provided("darwin"),
	"task":           provided("task"),
	"send_message":   provided("send_message"),
	"task_stop":      provided("task_stop"),
	"hide_messages":  provided("hide_messages"),
	"consult":        provided("consult"),
}

// Register adds the rows of set to reg in order. Rows whose dependencies are
// absent are skipped entirely (no registration, no always-on mark).
func Register(reg *tools.Registry, set []Entry, d *Deps) {
	if d.Registry == nil {
		d.Registry = reg
	}
	for _, e := range set {
		build, ok := catalog[e.Tool]
		if !ok {
			panic("assembly: unknown tool " + e.Tool)
		}
		specs, extraOn := build(d)
		for _, spec := range specs {
			reg.MustRegister(d.wrap(spec, e.Wrap))
			if e.AlwaysOn {
				reg.MarkAlwaysOn(spec.Name)
			}
		}
		for _, name := range extraOn {
			reg.MarkAlwaysOn(name)
		}
	}
}

func (d *Deps) wrap(spec tools.Tool, w Wrap) tools.Tool {
	if w&WrapCheckpoint != 0 && d.Checkpoint != nil {
		spec = d.Checkpoint(spec)
	}
	if w&WrapCodeIntel != 0 {
		switch {
		case d.CodeIntelWrap != nil:
			spec = d.CodeIntelWrap(spec)
		case d.CodeIntel != nil:
			spec = d.CodeIntel.WrapMutation(spec)
		}
	}
	return spec
}

// OrchestratorRegistry restricts a hard-orchestrator coordinator to
// delegation plus read-only lookups. host_power follows the coordinator only
// on interactive surfaces (it needs a human to confirm).
func OrchestratorRegistry(base *tools.Registry, interactive bool) *tools.Registry {
	root := agent.OrchestratorRegistry(base)
	if interactive {
		root.MustRegister(toolhostpower.Spec())
	}
	return root
}

// ChildToolset is the self-contained registry of a Darwin best-of-N
// candidate rooted at its git worktree. No darwin (no recursion), ask_user,
// goal, memory or tool_search, so everything is always-on. Shared verbatim by
// the TUI and the web GUI.
func ChildToolset(root string) *tools.Registry {
	reg := tools.NewRegistry()
	Register(reg, childSet, &Deps{Home: root})
	return reg
}

var childSet = alwaysOn(
	"read_image", "search_code", "read_zip", "read_docx", "read_xlsx", "read_pdf",
	"edit_docx", "edit_xlsx", "list_dir", "read_lines", "read_context", "read_many",
	"scratchpad", "patch_file", "create_file", "edit_files", "write_file", "make_dir", "move",
	"copy", "trash", "ctx_execute",
)

func alwaysOn(names ...string) []Entry {
	out := make([]Entry, len(names))
	for i, n := range names {
		out[i] = Entry{Tool: n, AlwaysOn: true}
	}
	return out
}
