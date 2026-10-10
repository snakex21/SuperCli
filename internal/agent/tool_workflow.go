package agent

import (
	"strings"

	"supercli/internal/llm"
)

const maxWorkflowTools = 16

// Continue an executed tool's registered workflow without another discovery
// call. Results and fetched pages cannot declare capabilities or permissions.
// Called on the loop owner only, after all parallel calls have completed.
func (l *Loop) learnWorkflowTools(calls []llm.ToolCall, outcomes []callOutcome) {
	if l.registry == nil || l.finalReplyOnly {
		return
	}
	for i, call := range calls {
		if i >= len(outcomes) || outcomes[i].failed || outcomes[i].inert {
			continue
		}
		if call.Name == invokeToolName {
			// A preceding tool_search in this batch may have activated the source
			// after the initial rewrite. Execution already succeeded; decode its
			// effective target with normal admission, without executing it again.
			resolved, err := resolveInvokeToolCall(l.registry, call)
			if err != nil {
				continue
			}
			call = resolved
		}
		spec, ok := l.registry.Get(call.Name)
		if !ok {
			continue
		}
		for _, name := range spec.NextTools {
			name = strings.TrimSpace(name)
			if name == "" || name == call.Name || name == invokeToolName || len(l.workflowTools) >= maxWorkflowTools {
				continue
			}
			if _, exists := l.registry.Get(name); !exists {
				continue
			}
			duplicate := false
			for _, previous := range l.workflowTools {
				duplicate = duplicate || previous == name
			}
			if !duplicate {
				l.workflowTools = append(l.workflowTools, name)
				l.workflowRevision++
			}
		}
	}
}
