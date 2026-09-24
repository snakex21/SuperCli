package llm

// Public Responses endpoints negotiate independently; a gateway rejecting an
// effort must not disable the same model on another endpoint. Keep the special
// Zen dialect and the ChatGPT backend's existing model-only key unchanged.
func (p *CodexProvider) reasoningKey() string {
	if p.cfg.StandardResponsesAPI && !isOpenCodeZenBaseURL(p.cfg.BackendURL) {
		return ReasoningSupportKey(p.cfg.BackendURL, p.cfg.Model)
	}
	return p.cfg.Model
}

func (p *CodexProvider) reasoningEffort() string {
	if p.cfg.StandardResponsesAPI && !isOpenCodeZenBaseURL(p.cfg.BackendURL) {
		return ReasoningEffortForModelWithCapability(p.reasoningKey(), true)
	}
	effort := ReasoningEffortForModel(p.cfg.Model)
	if effort == "none" {
		return ""
	}
	return effort
}

func (p *CodexProvider) supportsReasoningControl() bool {
	key := p.reasoningKey()
	if levels, learned := SupportedReasoningEfforts(key); learned {
		return len(levels) > 0
	}
	return p.reasoningEffort() != "" || SupportsReasoningEffort(p.cfg.Model) || p.caps.HasReasoning(p.cfg.Model)
}
