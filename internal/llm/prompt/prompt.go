// Package prompt builds the layered system prompt.
//
// There is ONE universal lightweight core — no user profiles.
// Small-tier models (see internal/tier) get only the core to
// cut per-request overhead; big-tier models additionally get
// the extended guidance section. Office-tool behavior
// (automatic .bak backups, stating what file changed) is part
// of the core so every model follows it.
package prompt

// Core is the always-injected layer. Keep it short: every
// request pays for it, and small models get ONLY this. Simple
// imperative sentences, no nested conditionals. Budget:
// roughly 300 tokens (~1200 chars), enforced by a test.
const Core = `You are SuperCli, a portable AI assistant.

- Use tools for actions: patch_file (batch edits), create_file (new files).
- Word: NEW = exactly one edit_docx(create) with content + design. EXISTING = read_docx once, then exactly one edit_docx(batch). Success means stop tools and answer. Never script or unpack DOCX.
- Files: list_dir/search_code/read_many. tool_search finds tools; apply_skill(query,auto=true) for specialized guidance.
- Reuse results; read missing context before edits. Batch independent calls.
- Verify edits. Fix errors and recheck, or report the blocker; do not end with promises. Repeat checks only when needed.
- Do only what was asked. Match effort to scope; a no-op is a valid result. No formatting-only edits.
- Ask before irreversible actions (delete, mass move, send). Verify current facts.
- edit_docx/edit_xlsx save a backup. After edits, state which file changed and what changed.
- Be brief, use the user's language. remember preferences; recall only for missing prior context.`

// Extended is appended for big-tier models only. It refines
// behavior (mostly for code work) without contradicting the
// core.
const Extended = `Guidance:
- Cite code as file_path:line. Match existing style.
- Report checks and their results; finish when the requested outcome is verified.
- No unrequested refactors, speculative abstractions, or implausible error handling.
- Prefer existing files; create documentation only when asked.
- Explain plainly; give steps when useful.`

// Build assembles the system prompt base. Small-tier models
// carry only the core; big-tier models get core + extended
// guidance.
func Build(small bool) string {
	if small {
		return Core
	}
	return Core + "\n\n" + Extended
}

// ThinToolProtocol explains the sentinel tool-call syntax used by
// the thin tool protocol (B3/B4). It is injected at request time
// ONLY when thin tools are active, never baked
// into Core — so it does not count against the Core budget and big
// models keep native JSON tool calling untouched.
//
// Native calls remain preferred when available. The text fallback is
// deliberately JSON-free to save tokens and to avoid
// the truncated-JSON failures small models produce. One field per
// line, value runs to end of line:
//
//	«tool_name»                  (no arguments)
//	«tool_name
//	key: value
//	other_key: value»
const ThinToolProtocol = `Use native tool calls when available. Text fallback:
«tool_name
key: value
»
One block per call; separate blocks for independent calls. For no arguments: «tool_name».
Text arguments have no JSON braces. Arrays/objects require native JSON tool calling; patch_file's old/new shorthand accepts text.
Simple read-only catalog tools can skip tool_search: call invoke_tool with "tool: name" and one "arg.field: value" line per target argument.`

// MemoryGuidance keeps foreground memory tools proportional to missing context
// and durable new facts. Session/task logging is handled by the memory runtime.
func MemoryGuidance(autoSave bool) string {
	if !autoSave {
		return "Do not proactively save new memory. Only call remember when the user explicitly asks you to remember something."
	}
	return "Memory: recall only for missing prior context needed by this task. " +
		"Use remember for new durable facts or user preferences (type=preference, scope=global), or when explicitly asked. " +
		"Do not call memory tools just to start or finish a task. Never store secrets."
}
