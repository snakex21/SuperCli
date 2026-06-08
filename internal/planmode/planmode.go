// Package planmode manages the /plan mode toggle. When active,
// the system appends a read-only instruction to the prompt so
// the LLM produces a structured plan instead of executing changes.
// This matches Claude Code's plan mode: instruction-based, not
// enforcement-based.
package planmode

// Suffix is appended to the user prompt when plan mode is active.
// The model follows this instruction and produces a structured
// plan instead of executing file changes.
const Suffix = `

[PLAN MODE ACTIVE]
Analyze the task and produce a structured plan. Do NOT make any file changes or run commands that modify state. Only read files and analyze.
Output your plan in this exact format:

## Plan
### Phase 1: <phase name>
- [ ] <step description>
- [ ] <step description>

### Phase 2: <phase name>
- [ ] <step description>

## Risks
- <potential risk>

## Estimated effort
- <time estimate>

When done, tell the user: "Plan ready. Type /plan to exit plan mode and start execution."`

// IsPlanPrompt returns true if the prompt contains the plan mode marker.
func IsPlanPrompt(prompt string) bool {
	return len(prompt) > 0 && contains(prompt, "[PLAN MODE ACTIVE]")
}

// WrapPrompt appends the plan mode suffix to the prompt.
func WrapPrompt(prompt string) string {
	return prompt + Suffix
}

// StatusLabel returns the label shown in the status bar.
func StatusLabel(active bool) string {
	if active {
		return "PLAN"
	}
	return ""
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
