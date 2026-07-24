package app

import (
	"os"
	"strings"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/llm/consult"
	"supercli/internal/llm/factory"
	"supercli/internal/llm/providers"
	"supercli/internal/system/config"
	"supercli/internal/tools"
)

// setupProviderManager creates the F30 provider manager, points active
// config writes at the highest-priority config file, reloads providers
// and registers the static Codex catalog for configured codex entries.
func setupProviderManager(dataDir, cwd string, caps *llm.CapabilityRegistry) *providers.Manager {
	// F30: create provider manager, load persisted
	// hidden-models state, and reload the providers list
	// from config.toml so model filtering works immediately.
	// (Created before /login so it can register the codex
	// provider entry.)
	provMgr := providers.NewManager(dataDir)
	// Persist the runtime /model selection to the highest-priority
	// config that startup resolution actually reads. When a project
	// config (<cwd>/.supercli/config.toml) is in effect it overrides
	// the global config at startup, so saving the selection only to
	// the global config would let the project config silently shadow
	// it — the model swap would be forgotten on the next launch.
	if gp, pp := config.FindTomlPaths(dataDir, cwd); pp != "" {
		if _, statErr := os.Stat(pp); statErr == nil {
			provMgr.SetActiveConfigPath(pp)
		} else {
			provMgr.SetActiveConfigPath(gp)
		}
	}
	provMgr.Reload()
	provMgr.LoadHiddenState()

	// ChatGPT-OAuth (codex) providers have no /v1/models endpoint,
	// so the background ScanModels alone could never discover their
	// models in past releases. Register the static Codex catalog for
	// every configured codex-type entry NOW, under the entry's own
	// name — otherwise the /model picker stays empty after a restart
	// (the catalog used to be registered only inside the /login
	// handler, and only under the hardcoded "codex" name, while the
	// onboarding wizard saves the entry as name "openai").
	for _, p := range provMgr.Configured() {
		if !p.Disabled && p.Type == config.ProviderCodex {
			llm.RegisterCodexCatalog(caps, p.Name)
		}
	}
	return provMgr
}

// makeCouncilMemberBuilder returns a function that builds a one-shot
// provider for a council roster spec ("providerName/modelID" or bare id).
func makeCouncilMemberBuilder(
	provMgr *providers.Manager,
	caps *llm.CapabilityRegistry,
	cfg config.Config,
	provFactory *factory.Factory,
) func(spec string) (llm.Provider, error) {
	// buildCouncilMember builds a one-shot provider for a
	// roster spec. The spec is "providerName/modelID" when the
	// prefix matches a configured provider (model ids may
	// themselves contain "/" or ":", e.g. openrouter/ollama);
	// otherwise the whole spec is treated as a bare model id
	// served by the active transport.
	return func(spec string) (llm.Provider, error) {
		provName, model := "", spec
		if i := strings.Index(spec, "/"); i > 0 {
			prefix := spec[:i]
			for _, pc := range provMgr.Configured() {
				if pc.Name == prefix && !pc.Disabled {
					provName, model = prefix, spec[i+1:]
					break
				}
			}
		}
		if provName == "" {
			provName = caps.Provider(model)
		}
		mCfg := cfg
		mCfg.Model = model
		for _, pc := range provMgr.Configured() {
			if pc.Name == provName && !pc.Disabled {
				mCfg.Provider = pc.Type
				mCfg.BaseURL = pc.BaseURL
				mCfg.APIKey = pc.APIKey
				break
			}
		}
		// Through the factory: council members are metered too, so
		// their calls land in the ledger under "consult" instead of
		// vanishing (WithPurpose on a raw provider was ignored).
		return provFactory.Build(mCfg, llm.PurposeConsult)
	}
}

// wireConsultTool builds the F12 council (auto cheapest-N or judge-only)
// and registers the consult tool with OnResult → loop.Emit.
func wireConsultTool(
	provider llm.Provider,
	caps *llm.CapabilityRegistry,
	cfg config.Config,
	provFactory *factory.Factory,
	buildMember func(string) (llm.Provider, error),
	loop *agent.Loop,
	registry *tools.Registry,
) *consult.Council {
	// The auto council (cheapest-N pool) stays as the fallback
	// for the consult tool and for /council when the user never
	// picked a roster.
	council := buildConsultCouncil(3, provider, caps, cfg, provFactory)
	if council == nil {
		// No cheap pool available — keep a judge-only council
		// so explicit model selection (tool `models` param and
		// the /council roster) still works.
		council = &consult.Council{Judge: provider}
	}
	consultTool := tools.NewConsult(council)
	consultTool.BuildProvider = buildMember
	consultTool.OnResult = func(r consult.Result) {
		winner := r.Verdict.WinnerIndex
		prov := ""
		if !r.AllFailed && winner >= 0 && winner < len(r.Candidates) {
			prov = r.Candidates[winner].Provider
		} else {
			winner = -1
		}
		loop.Emit(agent.ConsultEvent{
			Question:       r.Question,
			CandidateCount: len(r.Candidates),
			WinnerIndex:    winner,
			WinnerProvider: prov,
			Reason:         r.Verdict.Reason,
			AllFailed:      r.AllFailed,
			TotalTokens:    r.TotalTokens,
		})
	}
	registry.MustRegister(consultTool.Spec())
	return council
}
