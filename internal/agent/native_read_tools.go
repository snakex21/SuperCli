package agent

import (
	"sort"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

const (
	nativeReadToolLimit      = 8
	nativeReadToolBytes      = 8 << 10
	nativeReadToolProperties = 8
)

// selectNativeReadTools snapshots a bounded set of tools already admitted by
// the simple read-only dispatcher and named by registered core workflows.
// Selection neither activates tools nor changes the registry's permissions.
func selectNativeReadTools(reg *tools.Registry, isCore func(string) bool) []llm.ToolDef {
	if reg == nil {
		return nil
	}
	core := func(name string) bool { return isCore != nil && isCore(name) }
	names := reg.Names()
	sort.Strings(names)
	preferred := make(map[string]bool)
	for _, name := range names {
		if !core(name) {
			continue
		}
		if source, ok := reg.Get(name); ok {
			for _, target := range source.NextTools {
				preferred[target] = true
			}
		}
	}
	var selected []llm.ToolDef
	used := 0
	for _, name := range names {
		if !preferred[name] || core(name) {
			continue
		}
		tool, ok := reg.Get(name)
		if !ok || !isDirectToolEligible(tool) {
			continue
		}
		props, _ := flatScalarSchemaProperties(tool.Schema)
		if len(props) > nativeReadToolProperties {
			continue
		}
		size := len(tool.Name) + len(tool.Description) + len(tool.Schema)
		if size > nativeReadToolBytes-used {
			continue
		}
		selected = append(selected, llm.ToolDef{
			Name: tool.Name, Description: tool.Description, Schema: tool.Schema,
		})
		used += size
		if len(selected) == nativeReadToolLimit {
			break
		}
	}
	return selected
}
