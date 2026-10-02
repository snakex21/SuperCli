package agent

import (
	"strings"

	"supercli/internal/llm"
)

// Only the built-in dispatcher's description is projected. Its registered Spec
// remains exact; custom descriptions and other tool profiles are left alone.
func (l *Loop) projectInvokeToolDescription(defs []llm.ToolDef) {
	if !l.thinTools || !l.stableToolset || l.route != RouteCoordinator || l.orchestrator || l.registry == nil {
		return
	}
	// Recognition needs only the canonical schema/base, not a rebuilt catalog.
	builtin := NewInvokeTool(nil).Spec()
	const marker = " Eligible: "
	builtinBase, _, _ := strings.Cut(builtin.Description, marker)
	index := -1
	for i, def := range defs {
		if def.Name == invokeToolName && def.Schema == builtin.Schema {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	base, catalog, ok := strings.Cut(defs[index].Description, marker)
	if !ok || base != builtinBase {
		return
	}
	var remaining []string
	for _, signature := range strings.Split(catalog, "; ") {
		name, _, _ := strings.Cut(signature, "(")
		tool, registered := l.registry.Get(name)
		if !registered || !isDirectToolEligible(tool) || signature != directToolSignature(tool) {
			return // Preserve custom or outdated signature facts verbatim.
		}
		full := false
		for _, def := range defs {
			if def.Name == name && def.Schema == tool.Schema {
				full = true
				break
			}
		}
		if !full {
			remaining = append(remaining, signature)
		}
	}
	if len(remaining) > 0 {
		base += marker + strings.Join(remaining, "; ")
	}
	defs[index].Description = base
}
