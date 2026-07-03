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
	exploreSystem := "You are the SuperCli explore sub-agent. Your job is to " +
		"answer a focused question about the codebase. Use search_code " +
		"to locate files, then read them. Be concise: return only the " +
		"answer, no preamble."

	planSystem := "You are the SuperCli plan sub-agent. Your job is to " +
		"analyse a question and produce a numbered plan. Do not modify " +
		"files; do not call write tools. The plan should fit in 30 lines " +
		"and explicitly call out unknowns."

	reviewSystem := "You are the SuperCli review sub-agent. Your job is to " +
		"review code for correctness, performance, and clarity. Read the " +
		"targeted files with read_image (binary-safe) and search_code to " +
		"find call sites. Return findings as a bulleted list, ordered by " +
		"severity."

	codeSystem := "You are the SuperCli code sub-agent. Your job is to " +
		"implement the requested change end-to-end in your isolated context. " +
		"Read only the files you need, make targeted edits, run relevant " +
		"verification, and return a concise summary with files changed. " +
		"Do not spawn other sub-agents."

	// general is the default worker used when the coordinator calls
	// task with only a prompt (no explicit agent kind). It inherits the
	// full tool set (minus the delegation tools, which restrictedRegistry
	// strips so a worker can never nest another worker) so a single bare
	// prompt can drive real multi-tool work. The report is its final
	// message; keep it self-contained because the coordinator sees only
	// that text, never the worker's tool calls.
	generalSystem := "You are a SuperCli worker with an isolated context. " +
		"Carry out the delegated task end-to-end using your tools, then " +
		"return ONE self-contained report as your final message: what you " +
		"found or did, with concrete file paths and results. The coordinator " +
		"sees only this report, not your intermediate steps, so include " +
		"everything it needs. Be concise. Do not spawn other workers."

	return []SubAgent{
		{
			Name:        "general",
			Description: "carry out a delegated task end-to-end and report back",
			System:      generalSystem,
			// AllowedTools nil = inherit the full set (minus delegation).
			MaxSteps: 12,
		},
		{
			Name:         "explore",
			Description:  "search the codebase and answer a focused question",
			System:       exploreSystem,
			AllowedTools: allowedTools("search_code", "read_image"),
			MaxSteps:     8,
		},
		{
			Name:         "plan",
			Description:  "analyse a question and return a numbered plan",
			System:       planSystem,
			AllowedTools: allowedTools("read_image"),
			MaxSteps:     6,
		},
		{
			Name:         "review",
			Description:  "review existing code for correctness and clarity",
			System:       reviewSystem,
			AllowedTools: allowedTools("search_code", "read_image"),
			MaxSteps:     8,
		},
		{
			Name:        "code",
			Description: "implement a code change end-to-end",
			System:      codeSystem,
			AllowedTools: allowedTools(
				"search_code", "read_image", "read_lines", "read_context",
				"edit_line", "edit_lines", "insert_after", "delete_lines",
				"write_file", "make_dir", "move", "copy", "trash", "read_docx", "read_xlsx", "read_pdf",
				"read_zip", "edit_docx", "edit_xlsx", "ctx_execute",
			),
			MaxSteps: 12,
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
