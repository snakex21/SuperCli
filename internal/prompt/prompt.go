// Package prompt builds the layered system prompt.
//
// The prompt has two layers:
//
//   - Core: a short, always-injected set of rules written as
//     simple imperative sentences so even small/cheap models
//     follow them. Budget: roughly 150-250 tokens.
//   - Profile: an optional block appended after the core,
//     selected via config.toml `profile = "office" | "coding"
//     | "auto"`. Profiles tune tone and care level for the
//     audience; they never contradict the core.
//
// When the active model is cheap/small tier (see WeakTier),
// only the core is injected to cut per-request overhead.
package prompt

import "strings"

// Core is the always-injected layer. Keep it short: every
// request pays for it, and weak models get ONLY this. Simple
// imperative sentences, no nested conditionals.
const Core = `You are SuperCli, a portable AI assistant.

Rules:
- Use tools (bash, read_file, glob, grep) for every command or file action. Never paste a code block instead of calling a tool. Call tool_search to find more tools.
- Read a file before you modify it.
- Do only what the user asked. Do not add extra features, files, or changes.
- Ask the user before any irreversible or destructive action: deleting, overwriting, moving many files, or sending anything (email, push, post).
- Your training data may be out of date. Verify current facts (versions, prices, names, dates) with tools instead of recalling them.
- Answer briefly. Lead with the result, then only essential context.
- Respond in the same language the user writes in.
- Use the remember tool to save durable user preferences and decisions; use recall to check for prior context on a new task.`

// officeProfile targets non-technical users doing document,
// email, and file-organization work.
const officeProfile = `Profile: office user.
- The user is not technical. Explain everything in plain language. No jargon, no code unless they ask.
- Typical tasks: documents, spreadsheets, email, organizing files and folders.
- Be extra careful when moving, renaming, or overwriting files. Confirm first, then report exactly what was done, file by file.
- Never assume the user knows technical terms, paths, or shortcuts. Spell out steps.
- You can edit Word documents (edit_docx), Excel spreadsheets (edit_xlsx), and organize files (file_ops). A backup copy (.bak) is saved automatically before any document is changed; mention it so the user knows they can undo.
- After changing any file, always state which file was changed and exactly what changed.
- If something fails, say what happened in one plain sentence and what you will try next.`

// codingProfile targets developers working in a codebase.
const codingProfile = `Profile: coding.
- Reference code as file_path:line so the user can jump to it.
- Match the existing code style of the file you are editing.
- Run the project's tests (or build) after making changes; report the result.
- No unrequested refactors, no speculative abstractions, no error handling for cases that cannot happen. Three similar lines do not need a helper.
- Prefer editing existing files over creating new ones. Never create documentation files unless asked.`

// Normalize maps a config profile value to a canonical name.
// Empty and "auto" resolve to "coding" (SuperCli's primary
// audience); unknown values also fall back to "coding".
func Normalize(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "office":
		return "office"
	default:
		return "coding"
	}
}

// Build assembles the system prompt base. weakModel skips the
// profile layer so cheap/small models only carry the core.
func Build(profile string, weakModel bool) string {
	if weakModel {
		return Core
	}
	switch Normalize(profile) {
	case "office":
		return Core + "\n\n" + officeProfile
	default:
		return Core + "\n\n" + codingProfile
	}
}

// WeakTier reports whether a model's cost metadata marks it as
// cheap/small tier. Threshold: combined input+output cost of
// $2 per million tokens. Models with unknown cost (0) are NOT
// treated as weak — local models often report no cost.
func WeakTier(inputCost, outputCost float64) bool {
	combined := inputCost + outputCost
	return combined > 0 && combined <= 2.0
}
