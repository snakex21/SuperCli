package llm

// ReasoningState describes the control SuperCli will send on the next request,
// not proof of a backend's internal computation. It performs no network calls.
type ReasoningState struct {
	Configured string `json:"configured"`
	// Selected is the menu option representing the effective setting. Legacy
	// positive efforts on a toggle-only model all select the single On option.
	Selected   string   `json:"selected"`
	Effective  string   `json:"effective"`
	Adjusted   bool     `json:"adjusted"`
	Supported  bool     `json:"supported"`
	Levels     []string `json:"levels"`
	ToggleOnly bool     `json:"toggle_only,omitempty"`
	SupportKey string   `json:"-"`
}

func ProviderReasoningState(provider Provider) ReasoningState {
	state := ReasoningState{Configured: ReasoningEffort(), Levels: ReasoningEffortLevels, Supported: true}
	p := Unwrap(provider)
	if p == nil {
		state.Supported = false
		return state
	}
	state.SupportKey = p.Name()
	var caps *CapabilityRegistry
	var model string
	omitNone := false
	switch p := p.(type) {
	case *ResponsesProvider:
		return ProviderReasoningState(p.inner)
	case *OpencodeProvider:
		return ProviderReasoningState(p.inner)
	case *OpenAIProvider:
		state.SupportKey = p.reasoningKey()
		state.Effective = ReasoningEffortForModelWithCapability(state.SupportKey, p.reasoningFormat() != openAIReasoningNone)
		if isLocalBaseURL(p.cfg.BaseURL) && p.cfg.CapabilityModel == "" {
			caps, model = p.caps, p.capabilityModel()
		}
	case *CodexProvider:
		state.SupportKey = p.reasoningKey()
		state.Effective = p.reasoningEffort()
		omitNone = !p.cfg.StandardResponsesAPI || isOpenCodeZenBaseURL(p.cfg.BackendURL)
		if p.cfg.StandardResponsesAPI && isOpenCodeZenBaseURL(p.cfg.BackendURL) {
			// This special serializer intentionally sends the user dial directly.
			state.Effective = state.Configured
			if state.Effective == "none" {
				state.Effective = ""
			}
		} else if p.cfg.StandardResponsesAPI && isLocalBaseURL(p.cfg.BackendURL) {
			caps, model = p.caps, p.cfg.Model
		}
	case *AnthropicProvider:
		state.Effective = ReasoningEffortForModel(p.cfg.Model)
	default:
		state.Effective = ReasoningEffortForModelWithCapability(state.SupportKey, true)
	}
	if levels, learned := SupportedReasoningEfforts(state.SupportKey); learned {
		state.Supported = len(levels) > 0
		state.Levels = []string{}
		for _, level := range ReasoningEffortLevels {
			if hasEffort(levels, level) {
				state.Levels = append(state.Levels, level)
			}
		}
	}
	if caps != nil {
		info, _ := caps.Get(model)
		state.ToggleOnly = info.ReasoningToggleOnly
		if state.ToggleOnly && state.Effective != "" && state.Effective != "none" {
			state.Effective = "on"
		}
	}
	if state.ToggleOnly && state.Supported {
		state.Levels = []string{"none", "high"}
	}
	if omitNone {
		levels := make([]string, 0, len(state.Levels))
		for _, level := range state.Levels {
			if level != "none" {
				levels = append(levels, level)
			}
		}
		state.Levels = levels
	}
	if state.Configured != "" {
		state.Selected = state.Effective
		if state.ToggleOnly && state.Selected == "on" {
			state.Selected = "high"
		}
		if !hasEffort(state.Levels, state.Selected) {
			state.Selected = ""
		}
	}
	state.Adjusted = state.Configured != "" && state.Effective != "" && state.Configured != state.Effective
	return state
}
