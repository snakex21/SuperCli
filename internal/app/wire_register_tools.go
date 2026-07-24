package app

import (
	"log"
	"os"
	"strings"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/library"
	"supercli/internal/system/config"
	"supercli/internal/tools"
)

// toolWrappers optional wraps applied to mutating tools.
type toolWrappers struct {
	// checkpoint wraps file-mutating tools with undo snapshots.
	checkpoint func(tools.Tool) tools.Tool
	// diagnostic wraps mutations with code-intel invalidation.
	diagnostic func(tools.Tool) tools.Tool
}

func (w toolWrappers) mut(spec tools.Tool) tools.Tool {
	if w.checkpoint != nil {
		spec = w.checkpoint(spec)
	}
	if w.diagnostic != nil {
		spec = w.diagnostic(spec)
	}
	return spec
}

func (w toolWrappers) cp(spec tools.Tool) tools.Tool {
	if w.checkpoint != nil {
		return w.checkpoint(spec)
	}
	return spec
}

// registerMediaAndOfficeTools registers image/office/pdf/screenshot tools
// and the tool_search + skill/user-tool loaders. Callers own Close on the
// returned CodeIntel, ProcessSession and ftsClose.
func registerMediaAndOfficeTools(
	registry *tools.Registry,
	home, dataDir string,
	caps *llm.CapabilityRegistry,
	wrap toolWrappers,
) (askCh chan tools.AskRequest, codeIntel *tools.CodeIntel, processSession *tools.ProcessSession, toolSearcher *tools.ToolSearcher, ftsClose func()) {
	registry.MustRegister(tools.NewReadImage(home, 0).Spec())

	askCh = make(chan tools.AskRequest, 4)
	registry.MustRegister(tools.NewAskUser(askCh).Spec())

	registry.MustRegister(tools.NewSearchCode(home).Spec())
	codeIntel = tools.NewCodeIntel(home)
	// Mutating office tools need diagnostic wrap (code-intel invalidation)
	// in addition to checkpoint undo. Apply before those registrations.
	wrap.diagnostic = codeIntel.WrapMutation
	// Dormant by default: tool_search exposes its one compact schema only when
	// the model needs exact symbols, references or diagnostics.
	registry.MustRegister(codeIntel.Spec())
	processSession = tools.NewProcessSession(home)
	registry.MustRegister(processSession.Spec())

	// F21: read_zip is opt-in (not always-on).
	// The model discovers it via tool_search
	// when needed. The implementation is pure
	// stdlib (archive/zip + path/filepath), so
	// the binary stays self-contained.
	registry.MustRegister(tools.NewReadZip(home, 0).Spec())

	// F19: read_docx is opt-in (not always-on).
	// The model discovers it via tool_search
	// when needed. The implementation is pure
	// stdlib (archive/zip + encoding/xml); no
	// libreoffice, no docx library, no shell-out.
	registry.MustRegister(tools.NewReadDocx(home, 0).Spec())

	// F22: read_xlsx is opt-in (not always-on).
	// The model discovers it via tool_search
	// when needed. The implementation is pure
	// stdlib (archive/zip + encoding/xml); no
	// excelize, no libreoffice, no shell-out.
	// Renders cells as a markdown-style
	// pipe-separated table (one row per line,
	// cells joined by " | ").
	registry.MustRegister(tools.NewReadXlsx(home, 0).Spec())

	// Wave-2 office editing: edit_docx / edit_xlsx
	// rewrite a single zip entry byte-for-byte safe
	// (temp file + atomic swap + .bak backup), pure
	// stdlib. file_ops is the safe file manager for
	// office users: no overwrite, no hard delete
	// (trash folder instead), sandboxed paths.
	registry.MustRegister(wrap.mut(tools.NewEditDocx(home).Spec()))
	registry.MustRegister(wrap.mut(tools.NewEditXlsx(home).Spec()))
	registry.MustRegister(tools.NewListDir(home).Spec())

	// F20: read_pdf is opt-in (not always-on).
	// The model discovers it via tool_search
	// when needed. The implementation uses
	// github.com/ledongthuc/pdf (pure Go, no
	// cgo, no shell-out — no pdftotext, no
	// poppler). Pages are separated by
	// "--- Page N ---" headers.
	registry.MustRegister(tools.NewReadPdf(home, 0).Spec())

	// F23: send_screenshot is opt-in (not
	// always-on). The model discovers it via
	// tool_search when it needs to attach a
	// clipboard image to the next message.
	// The capture uses OS-specific commands
	// (PowerShell / osascript / xclip /
	// wl-paste) and the F16 vision gate
	// refuses the call if the current model
	// doesn't support image input.
	registry.MustRegister(tools.NewSendScreenshot(home, caps.HasVision).Spec())

	ftsIndex, err := tools.NewInMemoryIndex()
	if err != nil {
		fatal("init fts index", err)
	}
	ftsClose = func() { _ = ftsIndex.Close() }
	toolSearcher = tools.NewToolSearcher(registry, ftsIndex)
	registry.MustRegister(toolSearcher.Spec())

	discoverer := tools.NewDiscovererWithBuiltins(home, dataDir)
	skillApplier := tools.NewSkillApplier(discoverer)
	registry.MustRegister(skillApplier.Spec())

	userLoader := tools.NewUserToolLoader(home, dataDir)
	userTools, userErrs := userLoader.Load()
	for _, e := range userErrs {
		log.Printf("user tool load: %v", e)
	}
	for _, t := range userTools {
		registry.MustRegister(t)
	}
	return askCh, codeIntel, processSession, toolSearcher, ftsClose
}

// registerFileWebAndLineTools registers line-level file ops, web tools,
// library alternatives, invoke_tool and outlook_mail. Call after the
// agent loop and other late tools so tool_search reindex sees them.
func registerFileWebAndLineTools(
	registry *tools.Registry,
	home string,
	tomlCfg config.TomlConfig,
	wrap toolWrappers,
	toolSearcher *tools.ToolSearcher,
) {
	// F17: library alternatives tool. Opt-in
	// (NOT MarkAlwaysOn); the model discovers
	// it via tool_search. The Finder uses a
	// built-in catalog of ~25 curated mappings.
	libFinder := library.NewFinder()
	registry.MustRegister(tools.NewCheckLibraryAlternatives(libFinder).Spec())

	// F24: targeted file operations. All five
	// tools are opt-in (NOT MarkAlwaysOn); the
	// model discovers them via tool_search. They
	// save tokens by reading/editing specific line
	// ranges instead of entire files.
	registry.MustRegister(tools.NewReadLines(home).Spec())
	registry.MustRegister(tools.NewReadContext(home).Spec())
	registry.MustRegister(tools.NewReadMany(home).Spec())
	registry.MustRegister(tools.NewScratchpad(home).Spec())
	// Preferred mutations for the main model (also thin-core).
	registry.MustRegister(wrap.mut(tools.NewPatchFile(home).Spec()))
	registry.MustRegister(wrap.mut(tools.NewCreateFile(home).Spec()))
	// Legacy line editors stay registered for workers/compat; not always-on core.
	registry.MustRegister(wrap.mut(tools.NewEditLine(home).Spec()))
	registry.MustRegister(wrap.mut(tools.NewEditLines(home).Spec()))
	registry.MustRegister(wrap.mut(tools.NewInsertAfter(home).Spec()))
	registry.MustRegister(wrap.mut(tools.NewDeleteLines(home).Spec()))
	registry.MustRegister(wrap.mut(tools.NewWriteFile(home).Spec()))
	registry.MustRegister(wrap.mut(tools.NewMakeDir(home).Spec()))
	registry.MustRegister(wrap.mut(tools.NewMove(home).Spec()))
	registry.MustRegister(wrap.mut(tools.NewCopy(home).Spec()))
	registry.MustRegister(wrap.mut(tools.NewTrash(home).Spec()))

	// Web tools: web_fetch (SSRF-guarded HTML→text fetcher) and
	// web_search (DuckDuckGo by default — no key; Brave/Tavily/SearXNG
	// when [web_search] in config.toml or BRAVE_API_KEY /
	// TAVILY_API_KEY supplies a key). Both are opt-in (NOT
	// MarkAlwaysOn); the model discovers them via tool_search.
	registry.MustRegister(tools.NewWebFetch().Spec())
	wsEngine := tomlCfg.WebSearch.Engine
	wsKey := tomlCfg.WebSearch.APIKey
	if wsKey == "" {
		switch strings.ToLower(wsEngine) {
		case "brave":
			wsKey = os.Getenv("BRAVE_API_KEY")
		case "tavily":
			wsKey = os.Getenv("TAVILY_API_KEY")
		}
	}
	webSearcher := tools.NewWebSearch(wsEngine, wsKey, tomlCfg.WebSearch.BaseURL)
	registry.MustRegister(webSearcher.Spec())
	registry.MustRegister(webSearcher.LookupSpec())
	registry.MarkAlwaysOn("web_lookup")
	registry.MustRegister(agent.NewInvokeTool(registry).Spec())

	// outlook_mail: Windows-only COM automation of desktop
	// Outlook (read folders/messages, search, create DRAFTS —
	// never sends/deletes/moves). Opt-in via tool_search; on
	// non-Windows it returns an explanatory error.
	registry.MustRegister(tools.NewOutlookMail().Spec())

	// Re-index for tool_search: many tools (ctx_execute, goal,
	// memory, task, consult, file-line tools, web tools, ...)
	// are registered AFTER the first RebuildIndex call above, so
	// without this second pass they were invisible to tool_search
	// and effectively unreachable for small-tier models.
	if toolSearcher != nil {
		if err := toolSearcher.RebuildIndex(); err != nil {
			log.Printf("tool_search reindex: %v", err)
		}
	}
}
