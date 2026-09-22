package agent

import (
	"encoding/json"
	"strings"

	"supercli/internal/llm"
)

// standaloneDocxMutationFinalReply recognizes the complete-in-one-call Word
// paths encouraged by the universal prompt. It deliberately requires one
// successful, non-dry-run create or batch. Mixed tool batches remain ordinary
// agent steps, so an explicitly combined workflow is not cut short.
func standaloneDocxMutationFinalReply(calls []llm.ToolCall, outcomes []callOutcome) (string, bool) {
	if len(calls) != 1 || len(outcomes) != 1 || outcomes[0].failed || outcomes[0].inert {
		return "", false
	}
	call := calls[0]
	if call.Name != "edit_docx" {
		return "", false
	}
	var args struct {
		Action string `json:"action"`
		Path   string `json:"path"`
		DryRun bool   `json:"dry_run"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil || args.DryRun {
		return "", false
	}
	action := strings.ToLower(strings.TrimSpace(args.Action))
	if action != "create" && action != "batch" {
		return "", false
	}
	path := strings.TrimSpace(args.Path)
	if path == "" {
		return "✓ Word document ready.", true
	}
	return "✓ Word: " + path, true
}
