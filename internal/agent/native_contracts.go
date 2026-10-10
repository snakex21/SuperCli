package agent

import "supercli/internal/llm"

const (
	nativeContractLimit = 16
	nativeContractBytes = 16 << 10
)

// Native names avoid translating an already available contract through the
// generic dispatcher. Read contracts are fixed for the registry; initially
// requested contracts are fixed for this Run. Later discovery retains the
// existing dispatcher, instead of rewriting the provider's prefix each step.
func (l *Loop) nativeContracts() []llm.ToolDef {
	if !l.thinTools || !l.stableToolset || l.route != RouteCoordinator || l.finalReplyOnly || l.registry == nil {
		return nil
	}
	if !l.nativeReadsSet {
		l.nativeReads = selectNativeReadTools(l.registry, l.isSchemaCore)
		l.nativeReadsSet = true
	}
	if !l.nativeRunSet {
		var size int
		appendContract := func(def llm.ToolDef) {
			for _, previous := range l.nativeRunTools {
				if previous.Name == def.Name {
					return
				}
			}
			cost := len(def.Name) + len(def.Description) + len(def.Schema)
			if len(l.nativeRunTools) >= nativeContractLimit || size+cost > nativeContractBytes {
				return
			}
			l.nativeRunTools = append(l.nativeRunTools, def)
			size += cost
		}
		for _, def := range l.nativeReads {
			appendContract(def)
		}
		for _, def := range l.requestedToolDefinitions() {
			if !l.isSchemaCore(def.Name) {
				appendContract(def)
			}
		}
		l.nativeRunSet = true
	}
	return l.nativeRunTools
}

func containsNativeContract(defs []llm.ToolDef, name string) bool {
	for _, def := range defs {
		if def.Name == name {
			return true
		}
	}
	return false
}
