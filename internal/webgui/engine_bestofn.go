package webgui

import (
	"strings"

	"supercli/internal/agent/darwin"
	"supercli/internal/llm"
	"supercli/internal/system/config"
	"supercli/internal/system/execution"
	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
)

// registerDarwin exposes the darwin best-of-N tool in the GUI, mirroring the
// TUI wiring in app/main.go: darwin_parallel, darwin_models (round-robin
// candidate models), darwin_judge_model and darwin_auto_merge (off = the
// winner stays on its own branch). Discoverable only, so ordinary turns pay
// no schema cost.
func (e *Engine) registerDarwin(reg *tools.Registry, prov llm.Provider, cfg config.Config, tc config.TomlConfig, home, system string) {
	dt := darwin.NewDarwinTool(prov, reg, home, system)
	dt.SetReasoningEffort(tc.DarwinReasoningEffort)
	dt.SetPoolSize(tc.DarwinPoolSize)
	dt.SetLoopFactory(darwin.AgentLoopAdapter(reg, func(root string) (*tools.Registry, error) {
		return darwinChildRegistry(root), nil
	}))
	parallel, _ := execution.Parallel(cfg.BaseURL, tc.DarwinParallel)
	dt.SetSequential(!parallel)
	var models []llm.Provider
	for _, ref := range tc.DarwinModels {
		models = append(models, e.overrideProvider(tc, ref, llm.PurposeTask))
	}
	dt.SetModels(models)
	if jp := e.overrideProvider(tc, tc.DarwinJudgeModel, llm.PurposeDarwin); jp != nil {
		dt.SetJudgeProvider(jp)
	}
	dt.SetAllowAutoMerge(tc.DarwinAutoMerge != nil && *tc.DarwinAutoMerge)
	dt.SetResultSink(func(r darwin.Result) (string, error) { return e.saveBestOfNRun(home, r) })
	reg.MustRegister(dt.Spec())
}

// overrideProvider builds a "model" / "provider/model" reference through the
// metered factory; nil = use the active model (unset, same as active, or a
// failed build).
func (e *Engine) overrideProvider(tc config.TomlConfig, ref, purpose string) llm.Provider {
	c := e.modelOverrideConfig(tc, strings.TrimSpace(ref))
	if c == nil || c.Normalize() != nil {
		return nil
	}
	e.mu.RLock()
	f := e.factory
	e.mu.RUnlock()
	if f == nil {
		return nil
	}
	p, err := f.Build(*c, purpose)
	if err != nil {
		return nil
	}
	return p
}

// darwinChildRegistry roots a candidate's file tools at its git worktree so
// its edits land on its own branch. Same set as the TUI's
// buildChildToolRegistry: no darwin (no recursion), ask_user, goal or memory.
func darwinChildRegistry(root string) *tools.Registry {
	reg := tools.NewRegistry()
	for _, sp := range []tools.Tool{
		tools.NewReadImage(root, 0).Spec(),
		tools.NewSearchCode(root).Spec(),
		tools.NewReadZip(root, 0).Spec(),
		tools.NewReadDocx(root, 0).Spec(),
		tools.NewReadXlsx(root, 0).Spec(),
		tools.NewReadPdf(root, 0).Spec(),
		tools.NewEditDocx(root).Spec(),
		tools.NewEditXlsx(root).Spec(),
		tools.NewListDir(root).Spec(),
		tools.NewReadLines(root).Spec(),
		tools.NewReadContext(root).Spec(),
		tools.NewReadMany(root).Spec(),
		tools.NewScratchpad(root).Spec(),
		tools.NewPatchFile(root).Spec(),
		tools.NewCreateFile(root).Spec(),
		tools.NewWriteFile(root).Spec(),
		tools.NewMakeDir(root).Spec(),
		tools.NewMove(root).Spec(),
		tools.NewCopy(root).Spec(),
		tools.NewTrash(root).Spec(),
		tools.NewCtxExecuteTool(ctxexec.New(root), root).Spec(),
	} {
		reg.MustRegister(sp)
		reg.MarkAlwaysOn(sp.Name)
	}
	return reg
}
