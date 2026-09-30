package agent

import (
	"regexp"

	"supercli/internal/agent/ultrawork"
	"supercli/internal/llm"
)

// Old releases saved run-only system instructions in session history. Exclude
// only the exact generated forms from the model view, leaving the archive and
// user/tool mentions untouched. Otherwise /resume silently re-enables autonomy.
var legacySisyphusInstruction = regexp.MustCompile("^\\[Sisyphus @[0-9]+/[0-9]+\\] [0-9]+ todo\\(s\\) still open on the active /goal\\. Continue with the next one\\. Do NOT declare done until every task is `done` or explicitly `skipped` via the goal tool\\.$")

func isLegacyUltraworkInstruction(msg llm.Message) bool {
	return msg.Role == llm.RoleSystem && len(msg.Parts) == 0 &&
		(msg.Content == ultrawork.SystemPromptSection() || legacySisyphusInstruction.MatchString(msg.Content))
}
