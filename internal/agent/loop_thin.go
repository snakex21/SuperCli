package agent

import (
	"supercli/internal/llm"
	"supercli/internal/llm/prompt"
	"supercli/internal/tools"
)

// defaultThinHintMax caps catalog hints when ThinHintMax is 0. 80
// runes proved a good balance in measurement: ~84% token saving vs
// full schemas while keeping each hint a readable sentence.
const defaultThinHintMax = 80

// thinHintMaxOrDefault resolves the per-hint rune cap, falling back
// to defaultThinHintMax when unset. Shared by the catalog renderer
// and the /context accounting so both size the catalog identically.
func (l *Loop) thinHintMaxOrDefault() int {
	if l.thinHintMax <= 0 {
		return defaultThinHintMax
	}
	return l.thinHintMax
}

// SetRegistry swaps the tool registry the loop exposes to the model.
// Used at startup to hand the main loop a restricted (orchestrator)
// registry AFTER every tool — including the late-registered task tools —
// is present in the full base registry. The loop only reads its registry
// per-turn (buildToolDefs), so swapping the pointer before the first Run
// is safe and does not disturb any in-flight state.
func (l *Loop) SetRegistry(r *tools.Registry) {
	if r == nil {
		return
	}
	r.EnsureReadOutput()
	l.registry = r
	// The hoisted thin-tools preamble (stableToolset) renders from the
	// registry; a swap before the first Run must re-render it, not
	// serve a stale frozen copy.
	l.hoistedPreSet = false
	l.hoistedPre = ""
}

// VisibleToolNames returns the names of tools the model can currently see
// (the registry's visible + always-on set). Exposed for tests and
// diagnostics — in orchestrator mode this is the proof that mutating
// tools are physically absent from the main loop's registry.
func (l *Loop) VisibleToolNames() []string { return l.registry.VisibleNames() }

// buildToolDefs assembles the tool definitions sent to the
// provider for the current route. The coordinator route exposes
// every visible tool with its full JSON Schema; chat/advisor
// routes get only the minimal chatRouteTools set (tool_search +
// recall), letting the model pull in more on demand — that
// trimmed set is the per-turn token cost the router avoids.
//
// When thinTools is enabled, the coordinator set is trimmed to the
// full-schema core (thinCoreTools) plus any tool the model already
// pulled in via tool_search; the dormant tail is omitted here and
// advertised in the catalog (see toolCatalog). This is the thin
// tool protocol's token win.
func (l *Loop) buildToolDefs() []llm.ToolDef {
	var toolDefs []llm.ToolDef
	if l.route == RouteCoordinator {
		schema, _ := l.thinPartition()
		for _, t := range schema {
			toolDefs = append(toolDefs, llm.ToolDef{
				Name:        t.Name,
				Description: t.Description,
				Schema:      t.Schema,
			})
		}
		return toolDefs
	}
	for _, name := range chatRouteTools {
		if t, ok := l.registry.Get(name); ok {
			toolDefs = append(toolDefs, llm.ToolDef{
				Name:        t.Name,
				Description: t.Description,
				Schema:      t.Schema,
			})
		}
	}
	return toolDefs
}

// isActivated reports whether name was explicitly pulled in via
// tool_search this session (so it should carry a full schema).
func (l *Loop) isActivated(name string) bool {
	return l.registry.IsActive(name)
}

// thinPartition splits the coordinator's visible tools into the set
// that carries a full JSON Schema this turn (schema) and the dormant
// tail that is advertised in the compact catalog instead (tail).
//
// When thin tools are off it is the identity split: every visible
// tool goes to schema and tail is empty, so callers preserve the
// historical behaviour. A tool is schema-carrying when it is in the
// thin core OR was already activated via tool_search; otherwise it
// is dormant tail. This is the single source of truth for the
// core/tail decision — buildToolDefs, thinToolsPreamble, and the
// /context accounting all derive from it so they cannot drift.
//
// stableToolset changes the activation rule: activated tools stay
// in the tail, so the schema set (and therefore the request `tools`
// list, serialized at the very start of the prompt by chat
// templates) is byte-identical all session and the server-side KV
// prompt cache survives tool activations. The activated tool is
// still fully usable — its schema arrived as the tool_search result
// text and Registry.Execute dispatches by name, not by promotion.
func (l *Loop) thinPartition() (schema, tail []tools.Tool) {
	for _, t := range l.registry.Visible() {
		if l.thinTools && !l.isSchemaCore(t.Name) {
			if l.stableToolset || !l.isActivated(t.Name) {
				tail = append(tail, t)
				continue
			}
		}
		schema = append(schema, t)
	}
	return schema, tail
}

// isSchemaCore reports whether name belongs to the always-full-schema
// core for the active mode. Orchestrator mode uses its own core set
// (delegation-first: task is core, the mutating tools are absent from
// the registry entirely); every other mode uses thinCoreTools.
func (l *Loop) isSchemaCore(name string) bool {
	if l.orchestrator {
		return isOrchestratorCore(name)
	}
	return isThinCore(name)
}

// thinToolsPreamble builds the request-time system block for the
// thin tool protocol: the sentinel call-format instruction (so the
// model knows to write «name\nkey: value» instead of JSON) plus,
// when the dormant tail is non-empty, a compact name+hint catalog
// of tools reachable via tool_search. Returns "" when thin tools
// are off or off the coordinator route, so callers inject nothing.
//
// The instruction is always present under thin tools (it governs
// how even the core tools are called); the catalog is appended
// only when there is a tail to advertise.
func (l *Loop) thinToolsPreamble() string {
	if !l.thinTools || l.route != RouteCoordinator {
		return ""
	}
	out := prompt.ThinToolProtocol

	_, tail := l.thinPartition()
	var direct, loadable []tools.Tool
	for _, tool := range tail {
		if isDirectToolEligible(tool) {
			direct = append(direct, tool)
		} else {
			loadable = append(loadable, tool)
		}
	}
	if body := tools.RenderCatalog(direct, l.thinHintMaxOrDefault()); body != "" {
		out += "\n\nSimple read-only tools, callable now through invoke_tool " +
			"(tool: name, arg.<field>: value):\n" + body
	}
	if len(loadable) > 0 {
		if body := tools.RenderCatalog(loadable, l.thinHintMaxOrDefault()); body != "" {
			instruction := "More tools, loadable on demand — call tool_search with a " +
				"natural-language query to load any (it returns the full schema so " +
				"you can call it the same turn)"
			if l.stableToolset {
				instruction = "More tools, loadable on demand — call tool_search once " +
					"with a natural-language query. It activates the match and returns its " +
					"full schema; then call the target through invoke_tool with those args"
			}
			out += "\n\n" + instruction + ":\n" + body
		}
	}
	return out
}
