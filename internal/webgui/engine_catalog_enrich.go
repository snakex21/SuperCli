package webgui

import (
	"context"

	"supercli/internal/llm"
)

// enrichConfiguredProviderCatalogs fills context windows for the saved model
// inventory of gateways whose /models omits them (e.g. api.cline.bot). The GUI
// does not rescan providers on startup when cached_models exist, so without
// this the registry knows those ids only by name and the stats panel shows the
// generic remote fallback until some later request happens to refresh metadata.
// One bounded, cached catalog fetch per gateway; failures change nothing.
func (e *Engine) enrichConfiguredProviderCatalogs(ctx context.Context) {
	if e == nil {
		return
	}
	e.mu.RLock()
	caps := e.caps
	e.mu.RUnlock()
	if caps == nil {
		return
	}
	for _, p := range e.providerManager().Configured() {
		if p.Disabled || len(p.CachedModels) == 0 || llm.CatalogEnrichmentURL(p.BaseURL) == "" {
			continue
		}
		infos := make([]llm.ModelInfo, 0, len(p.CachedModels))
		for _, id := range p.CachedModels {
			info, ok := caps.Get(id)
			if !ok {
				info = llm.HeuristicCapabilities(id)
				info.Provider = p.Name
			}
			infos = append(infos, info)
		}
		for _, info := range llm.EnrichModelInfos(ctx, p.BaseURL, infos) {
			if info.ContextLength > 0 || info.InputCost > 0 || info.OutputCost > 0 {
				// Fills only missing values; explicit overrides are resolved
				// before the catalog and are never stored here.
				applyOneWebModelInfo(caps, info)
			}
		}
	}
}
