package webgui

import (
	"context"
	"strings"

	"supercli/internal/agent/darwin"
	"supercli/internal/assembly"
	"supercli/internal/llm"
	llmprompt "supercli/internal/llm/prompt"
	"supercli/internal/system/config"
	"supercli/internal/system/execution"
	"supercli/internal/tools"
)

// darwinSpec builds the darwin best-of-N tool for the GUI, mirroring the
// TUI wiring in app/main.go: darwin_parallel, darwin_models (round-robin
// candidate models), darwin_judge_model and darwin_auto_merge (off = the
// winner stays on its own branch). Discoverable only, so ordinary turns pay
// no schema cost.
func (e *Engine) darwinSpec(reg *tools.Registry, prov llm.Provider, cfg config.Config, tc config.TomlConfig, home, system string) tools.Tool {
	dt := darwin.NewDarwinTool(prov, reg, home, system)
	instructionsDataDir := e.dataDir
	dt.SetUserInstructionsForRun(func(ctx context.Context) (string, error) {
		return llmprompt.UserInstructionsForRun(ctx, instructionsDataDir)
	})
	dt.SetReasoningEffort(tc.DarwinReasoningEffort)
	dt.SetPoolSize(tc.DarwinPoolSize)
	dt.SetLoopFactory(darwin.AgentLoopAdapter(reg, func(root string) (*tools.Registry, error) {
		return assembly.ChildToolset(root), nil
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
	return dt.Spec()
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
