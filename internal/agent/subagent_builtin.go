package agent

import (
	"supercli/internal/llm"
)

// BuiltinSubAgents returns the four default sub-agent specs
// promised by the F2 design (see §4.5). The system prompts
// are intentionally short: the model only needs to know
// "what kind of work am I doing", not a full persona.
//
// All four inherit the parent's model and tool restrictions
// (Model = "" means inherit; AllowedTools = nil means inherit
// the full set). The defaults are good for a 32k-context
// model; F5 will tune them per-model once we have real
// benchmark data.
func BuiltinSubAgents() []SubAgent {
	const evidence = " Batch independent calls; read_many for files/ranges. Reuse evidence. Fix errors and recheck, or report the blocker; never end with promised work."
	exploreSystem := "You are the SuperCli explore worker. Answer the focused codebase question with precise paths/lines. Use web_lookup/web_fetch/web_search for current external documentation, read_image only for images. Return a concise answer." + evidence
	planSystem := "You are the SuperCli plan worker. Read-only: produce a numbered plan within 30 lines, including unknowns." + evidence
	reviewSystem := "You are the SuperCli review worker. Read-only: inspect targeted code and call sites for correctness, performance, and clarity. Report findings by severity with paths/lines." + evidence
	codeSystem := "You are the SuperCli code worker. Implement the requested change, run relevant checks, then report changed files and results. Match effort to scope; no-op is valid, no formatting-only edits. The workspace is your cwd; use relative paths and file/search tools. ctx_execute takes an argv list, not shell syntax. Do not spawn workers." + evidence

	// The default worker inherits tools, excluding delegation. Its final report
	// is the only worker message returned to the coordinator.
	generalSystem := "You are a SuperCli worker. Finish the delegated task using tools. Return one concise, self-contained report: findings, changed files, checks/results, and unresolved issues. The coordinator sees only this report. The workspace is your cwd; use relative paths and file/search tools. Match effort to scope; no-op is valid, no formatting-only edits. Do not spawn workers." + evidence

	// Selected explicitly by task's advise flag; the registry enforces read-only.
	advisorSystem := "You are a read-only SuperCli advisor. Answer the specific question: recommendation first, then a brief rationale. Use web_lookup/web_fetch/web_search for current external documentation when needed." + evidence

	return []SubAgent{
		{
			Name:        "general",
			Description: "carry out a delegated task end-to-end and report back",
			System:      generalSystem,
			// AllowedTools nil = inherit the full set (minus delegation).
		},
		{
			Name:        "advisor",
			Description: "give a read-only second opinion on a specific decision",
			System:      advisorSystem,
			AllowedTools: allowedTools(
				"search_code", "read_image", "read_lines", "read_many", "read_context", "list_dir", "scratchpad",
				"web_lookup", "web_fetch", "web_search", "tool_search",
			),
		},
		{
			Name:        "explore",
			Description: "search the codebase and answer a focused question",
			System:      exploreSystem,
			AllowedTools: allowedTools(
				"search_code", "read_image", "read_lines", "read_many", "read_context", "list_dir", "scratchpad",
				"web_lookup", "web_fetch", "web_search", "tool_search",
			),
		},
		{
			Name:         "plan",
			Description:  "analyse a question and return a numbered plan",
			System:       planSystem,
			AllowedTools: allowedTools("search_code", "read_image", "read_lines", "read_many", "read_context", "list_dir", "scratchpad"),
		},
		{
			Name:         "review",
			Description:  "review existing code for correctness and clarity",
			System:       reviewSystem,
			AllowedTools: allowedTools("search_code", "read_image", "read_lines", "read_many", "read_context", "list_dir", "scratchpad"),
		},
		{
			Name:        "code",
			Description: "implement a code change end-to-end",
			System:      codeSystem,
			AllowedTools: allowedTools(
				"search_code", "read_image", "read_lines", "read_many", "read_context", "list_dir",
				"patch_file", "create_file",
				"write_file", "make_dir", "move", "copy", "trash", "read_docx", "read_xlsx", "read_pdf",
				"read_zip", "edit_docx", "edit_xlsx", "ctx_execute", "scratchpad",
			),
		},
	}
}

// MustRegisterAll registers a list of sub-agents, panicking
// on the first error. Use in main.go and tests where the
// inputs are trusted.
func MustRegisterAll(reg *SubAgentRegistry, specs []SubAgent) {
	for _, s := range specs {
		reg.MustRegister(s)
	}
}

// promptFromChild returns the seed message that we want a
// child loop to start with. It is used by the F4 AgentTool
// runner to build a deterministic prompt for a unit test.
func promptFromChild(p string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: p}
}
