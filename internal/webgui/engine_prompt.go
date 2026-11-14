// Package webgui serves a local, dark-themed web GUI for SuperCli.
//
// It reuses the existing core packages (agent loop, providers,
// sessions, credits, goals, memory) through their public APIs, so
// the GUI is a real front-end over the same engine the TUI drives —
// not a mock. The server is pure net/http + embedded assets; no CGO,
// no new dependencies, keeping the single-binary, portable contract.
package webgui

import (
	"fmt"
	"strings"

	"supercli/internal/assembly"
	llmprompt "supercli/internal/llm/prompt"
	"supercli/internal/storage/goal"
	"supercli/internal/tools/sandbox"
)

func webAgentSystemPrompt(home, dataDir, model string, promptSmall, orchestrator, delegation bool, goalSvc *goal.Service, appProfile string) string {
	system, _ := webAgentSystemPromptLayers(home, dataDir, model, promptSmall, orchestrator, delegation, goalSvc, appProfile)
	return system
}

func webAgentSystemPromptLayers(home, dataDir, model string, promptSmall, orchestrator, delegation bool, goalSvc *goal.Service, appProfile string) (string, string) {
	officeProfile := strings.EqualFold(strings.TrimSpace(appProfile), "nestcafe")
	spec := assembly.PromptSpec{
		Base:         llmprompt.Build(promptSmall),
		ModelProfile: llmprompt.LoadProfileAt(home, dataDir, model),
		Goals:        goalSvc,
	}
	if officeProfile {
		// The extended SuperCli layer is intentionally code-oriented. NestCafe
		// uses the universal core, no repository instructions, and adds its
		// office behaviour as a surface layer.
		spec.Base = llmprompt.Core
	} else {
		spec.Project = llmprompt.LoadProjectInstructions(home)
	}
	switch {
	case orchestrator:
		spec.Role = assembly.RoleOrchestrator
	case delegation:
		spec.Role = assembly.RoleCoordinator
	}
	// Deliberately absent compared with the TUI: OS/staleness hints, the
	// embedded user preset and the memory briefing (all ride the per-request
	// live context instead), and the workspace note (per project, would split
	// the cacheable prefix across folders; see webWorkspaceNote).
	if delegation {
		spec.SurfaceLayers = append(spec.SurfaceLayers, assembly.WebSyncTaskNote)
	}
	if officeProfile {
		spec.SurfaceLayers = append(spec.SurfaceLayers, assembly.NestCafeOfficeMode)
	}
	spec.SurfaceLayers = append(spec.SurfaceLayers, assembly.GoalDiscoveryHint)
	return assembly.BuildSystemPrompt(spec), spec.Project
}

// webWorkspaceNote states the real sandbox root and access mode. It is sent in
// the request tail (live context), so the stable system prefix stays
// byte-identical across projects and remains cacheable.
func webWorkspaceNote(home string, officeProfile bool) string {
	if officeProfile && sandbox.IsUnsandboxed() {
		return fmt.Sprintf("Technical working directory: %s\nFull access to ordinary user files is ON. File, document, search, and command tools may use absolute paths such as Desktop, Documents, Downloads, Pictures, and user-named folders. Sensitive system folders remain blocked.", home)
	} else if sandbox.IsUnsandboxed() {
		return fmt.Sprintf("Active workspace: %s\nFull filesystem access is ON. File and search tools may use absolute paths outside this workspace; sensitive system folders remain blocked.", home)
	}
	return fmt.Sprintf("Active workspace (all file and shell tools are sandboxed here): %s", home)
}

// wireTaskTool registers the task / send_message / task_stop tools on
// reg, honouring the config knobs the CLI honours: task_max_steps,
// task_max_tokens, task_model (worker backend override) and
// preflight_repo (cold-context repo briefing for workers).
