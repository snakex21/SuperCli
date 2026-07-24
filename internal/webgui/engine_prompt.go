// Package webgui serves a local, dark-themed web GUI for SuperCli.
//
// It reuses the existing core packages (agent loop, providers,
// sessions, credits, goals, memory) through their public APIs, so
// the GUI is a real front-end over the same engine the TUI drives —
// not a mock. The server is pure net/http + embedded assets; no CGO,
// no new dependencies, keeping the single-binary, portable contract.
package webgui

import (
	"context"
	"fmt"
	"strings"

	"supercli/internal/agent"
	llmprompt "supercli/internal/llm/prompt"
	"supercli/internal/storage/goal"
	"supercli/internal/tools/sandbox"
)

func webAgentSystemPrompt(home, dataDir, model string, promptSmall, orchestrator, delegation bool, goalSvc *goal.Service, appProfile string) string {
	officeProfile := strings.EqualFold(strings.TrimSpace(appProfile), "nestcafe")
	system := llmprompt.Build(promptSmall)
	if officeProfile {
		// The extended SuperCli layer is intentionally code-oriented. NestCafe
		// uses the universal core and adds its office behavior below.
		system = llmprompt.Core
	}
	if profile := llmprompt.LoadProfileAt(home, dataDir, model); profile != "" {
		system += "\n\n" + profile
	}
	if orchestrator {
		system += agent.OrchestratorPrompt()
	} else if delegation {
		system += agent.CoordinatorPrompt()
	}
	// An HTTP/SSE run ends with the parent turn. Background task notifications
	// cannot be delivered to a closed response, so web coordinators deliberately
	// use synchronous task calls.
	if delegation {
		system += "\n\nWeb GUI: call task synchronously; do not request async/background workers."
	}
	if officeProfile && sandbox.IsUnsandboxed() {
		system += fmt.Sprintf("\n\nTechnical working directory: %s\nFull access to ordinary user files is ON. File, document, search, and command tools may use absolute paths such as Desktop, Documents, Downloads, Pictures, and user-named folders. Sensitive system folders remain blocked.", home)
	} else if sandbox.IsUnsandboxed() {
		system += fmt.Sprintf("\n\nActive workspace: %s\nFull filesystem access is ON. File and search tools may use absolute paths outside this workspace; sensitive system folders remain blocked.", home)
	} else {
		system += fmt.Sprintf("\n\nActive workspace (all file and shell tools are sandboxed here): %s", home)
	}
	if officeProfile {
		system += `

NestCafe office mode:
- Act as a general desktop and office assistant, not primarily as a programming agent.
- For requests about the user's Desktop, Documents, Downloads, pictures, or another named location, inspect that location directly with file tools. Full filesystem access covers ordinary user files; do not claim that the active workspace prevents access.
- Prefer the dedicated Word, Excel, PDF, image, archive, and file tools for document work. Preserve formatting and existing files unless the user asks for a conversion or replacement.
- Organizing, renaming, copying, and summarizing files are normal tasks. Before a bulk move, overwrite, or removal, show the intended scope and ask for confirmation. Use recoverable trash instead of hard deletion.
- Treat source-code and repository workflows as exceptional: use them only when the user explicitly asks for programming work.`
	}
	// A tiny discovery hint is cheaper than carrying the complete goal schema in
	// every request. When a goal is active, inject its open steps directly.
	system += "\n\nFor durable multi-step work, find and use the goal tool."
	if goalSvc != nil {
		if injected, err := goalSvc.Inject(context.Background(), system, 5); err == nil {
			system = injected
		}
	}
	return system
}

// wireTaskTool registers the task / send_message / task_stop tools on
// reg, honouring the config knobs the CLI honours: task_max_steps,
// task_max_tokens, task_model (worker backend override) and
// preflight_repo (cold-context repo briefing for workers).
