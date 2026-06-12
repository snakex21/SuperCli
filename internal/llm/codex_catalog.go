package llm

// CodexCatalog lists the ChatGPT-backend (Codex) models made
// available by a ChatGPT-subscription login. The ChatGPT backend
// has no public model-list endpoint (the Codex CLI itself ships a
// static list), so these mirror the models Codex offers as of
// June 2026: gpt-5.5 is the current default, gpt-5.4-mini the
// cheap/fast option, gpt-5.3-codex-spark the low-latency preview.
// The old gpt-5 / gpt-5-codex / gpt-5-mini family is retired and
// must not be seeded — picking it produces a "model not found"
// error.
//
// providerName is the configured provider entry name these models
// belong to ("codex" for /login, but the onboarding wizard saves
// the entry as "openai" with type "codex" — the /model picker
// filters rows by the configured provider NAME, so the catalog
// must be registered under whatever name the entry actually has).
func CodexCatalog(providerName string) []ModelInfo {
	if providerName == "" {
		providerName = "codex"
	}
	mk := func(id string) ModelInfo {
		return ModelInfo{
			ID: id, Provider: providerName,
			Vision: true, ToolUse: true, Stream: true, Reasoning: true,
			ContextLength: 272000,
			Notes:         "ChatGPT subscription (Codex login)",
			Source:        SourceCatalog,
		}
	}
	return []ModelInfo{mk("gpt-5.5"), mk("gpt-5.4-mini"), mk("gpt-5.3-codex-spark")}
}

// RegisterCodexCatalog merges the Codex catalog into the registry
// and returns the registered model ids.
//
// A plain RegisterAll is NOT enough: a stale probe-cache row for
// e.g. "gpt-5.5" loads with SourceProbe (higher priority than
// SourceCatalog) and an empty Provider — RegisterAll would then
// skip the catalog entry and the /model picker, which filters by
// provider name, would never show the model. So existing entries
// are merged field-by-field, always forcing the Provider name.
func RegisterCodexCatalog(r *CapabilityRegistry, providerName string) []string {
	catalog := CodexCatalog(providerName)
	ids := make([]string, 0, len(catalog))
	for _, m := range catalog {
		if existing, ok := r.Get(m.ID); ok {
			existing.Provider = m.Provider
			existing.ToolUse = true
			existing.Stream = true
			existing.Vision = existing.Vision || m.Vision
			existing.Reasoning = existing.Reasoning || m.Reasoning
			if existing.ContextLength == 0 {
				existing.ContextLength = m.ContextLength
			}
			if existing.Notes == "" {
				existing.Notes = m.Notes
			}
			if existing.Source == SourceSeed || existing.Source == SourceUnknown {
				existing.Source = m.Source
			}
			r.Register(existing)
		} else {
			r.Register(m)
		}
		ids = append(ids, m.ID)
	}
	return ids
}
