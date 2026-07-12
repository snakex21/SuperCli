package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

const invokeToolName = "invoke_tool"

const maxDirectCatalogEntries = 16

// InvokeTool is a schema-stable dispatcher for simple read-only tools. It
// removes the tool_search round-trip without adding every target's full schema
// to the request. Complex or mutating tools are deliberately ineligible.
type InvokeTool struct{ registry *tools.Registry }

func NewInvokeTool(registry *tools.Registry) *InvokeTool { return &InvokeTool{registry: registry} }

func (t *InvokeTool) Spec() tools.Tool {
	eligible := directToolCatalog(t.registry)
	description := "Call a simple read-only tool directly without tool_search. Native calls put target arguments in args; thin/sentinel calls use arg.<name> fields."
	if eligible != "" {
		description += " Eligible: " + eligible
	}
	return tools.Tool{
		Name:        invokeToolName,
		Description: description,
		ReadOnly:    true,
		Schema:      `{"type":"object","properties":{"tool":{"type":"string"},"args":{"type":"object","description":"Target arguments for native tool calling"}},"required":["tool"],"additionalProperties":true}`,
		Fn: func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
			_, err := resolveInvokeToolCall(t.registry, llm.ToolCall{Name: invokeToolName, Arguments: string(raw)})
			if err == nil {
				// Valid calls are rewritten by the loop before dispatch. Reaching
				// this function would mean an embedder bypassed that contract.
				err = fmt.Errorf("invoke_tool: dispatcher was not resolved by the agent loop")
			}
			return tools.Result{Err: err}, nil
		},
	}
}

func directToolCatalog(registry *tools.Registry) string {
	if registry == nil {
		return ""
	}
	var signatures []string
	for _, name := range registry.Names() {
		tool, ok := registry.Get(name)
		if !ok || !isDirectToolEligible(tool) {
			continue
		}
		signatures = append(signatures, directToolSignature(tool))
	}
	sort.Strings(signatures)
	if len(signatures) > maxDirectCatalogEntries {
		signatures = signatures[:maxDirectCatalogEntries]
	}
	return strings.Join(signatures, "; ")
}

func directToolSignature(tool tools.Tool) string {
	props, ok := flatScalarSchemaProperties(tool.Schema)
	if !ok || len(props) == 0 {
		return tool.Name
	}
	keys := make([]string, 0, len(props))
	for key := range props {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i, key := range keys {
		typ := props[key]
		switch typ {
		case "integer":
			typ = "int"
		case "boolean":
			typ = "bool"
		case "number":
			typ = "num"
		}
		keys[i] = key + ":" + typ
	}
	return tool.Name + "(" + strings.Join(keys, ",") + ")"
}

func isDirectToolEligible(tool tools.Tool) bool {
	if !tool.ReadOnly || tool.Name == "" || tool.Name == invokeToolName || tool.Name == "tool_search" {
		return false
	}
	_, ok := flatScalarSchemaProperties(tool.Schema)
	return ok
}

// flatScalarSchemaProperties accepts both standard JSON Schema
// {properties:{...}} and SuperCli's historical compact {field:{type:...}}
// shape. Nested objects/arrays/unions stay behind tool_search.
func flatScalarSchemaProperties(schema string) (map[string]string, bool) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(schema), &root); err != nil {
		return nil, false
	}
	propsRaw, standard := root["properties"]
	if standard {
		var props map[string]json.RawMessage
		if err := json.Unmarshal(propsRaw, &props); err != nil {
			return nil, false
		}
		root = props
	}
	props := make(map[string]string)
	for name, raw := range root {
		if !standard && (name == "type" || name == "required" || name == "additionalProperties") {
			continue
		}
		var spec map[string]json.RawMessage
		if json.Unmarshal(raw, &spec) != nil {
			return nil, false
		}
		if _, complex := spec["oneOf"]; complex {
			return nil, false
		}
		if _, complex := spec["anyOf"]; complex {
			return nil, false
		}
		var typ string
		if json.Unmarshal(spec["type"], &typ) != nil {
			return nil, false
		}
		switch typ {
		case "string", "integer", "number", "boolean":
			props[name] = typ
		default:
			return nil, false
		}
	}
	return props, true
}

func resolveInvokeToolCall(registry *tools.Registry, call llm.ToolCall) (llm.ToolCall, error) {
	if registry == nil {
		return call, fmt.Errorf("invoke_tool: registry unavailable")
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(call.Arguments), &envelope); err != nil {
		return call, fmt.Errorf("invoke_tool: bad args: %w", err)
	}
	var target string
	if err := json.Unmarshal(envelope["tool"], &target); err != nil || strings.TrimSpace(target) == "" {
		return call, fmt.Errorf("invoke_tool: tool is required")
	}
	target = strings.TrimSpace(target)
	tool, ok := registry.Get(target)
	if !ok {
		return call, fmt.Errorf("invoke_tool: unknown tool %q", target)
	}
	allowed, eligible := flatScalarSchemaProperties(tool.Schema)
	if !eligible || !tool.ReadOnly || target == invokeToolName || target == "tool_search" {
		return call, fmt.Errorf("invoke_tool: %s requires tool_search (not a simple read-only tool)", target)
	}

	args := make(map[string]json.RawMessage)
	if raw := envelope["args"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return call, fmt.Errorf("invoke_tool: args must be an object: %w", err)
		}
	}
	for key, value := range envelope {
		if !strings.HasPrefix(key, "arg.") {
			continue
		}
		name := strings.TrimPrefix(key, "arg.")
		if _, exists := args[name]; exists {
			return call, fmt.Errorf("invoke_tool: duplicate argument %q", name)
		}
		args[name] = value
	}
	for name := range args {
		if _, ok := allowed[name]; !ok {
			return call, fmt.Errorf("invoke_tool: argument %q is not valid for %s", name, target)
		}
	}
	rawArgs, err := json.Marshal(args)
	if err != nil {
		return call, fmt.Errorf("invoke_tool: encode target args: %w", err)
	}
	call.Name = target
	call.Arguments = string(rawArgs)
	return call, nil
}

func (l *Loop) resolveInvokeToolCalls(calls []llm.ToolCall) []llm.ToolCall {
	for i := range calls {
		if calls[i].Name != invokeToolName {
			continue
		}
		if resolved, err := resolveInvokeToolCall(l.registry, calls[i]); err == nil {
			calls[i] = resolved
		}
	}
	return calls
}
