package llm

import (
	"encoding/json"
	"strconv"
	"strings"
)

func normalizeAnthropicToolSchemaChecked(raw string) (json.RawMessage, error) {
	return cachedToolSchema(raw, toolSchemaAnthropic)
}

// Messages requires an object root without combinators. Keep ordinary root
// properties intact and project branch-only properties conservatively: a union
// branch that permits arbitrary extra properties must still permit that value.
// The execution registry continues to validate the unmodified full schema.
func rewriteAnthropicToolSchema(root map[string]any) {
	hasCombinator := false
	for _, key := range []string{"oneOf", "allOf", "anyOf"} {
		if _, ok := root[key]; ok {
			hasCombinator = true
		}
	}
	if !hasCombinator {
		return
	}
	preserveAnthropicRootReferences(root)
	names := make(map[string]struct{})
	collectAnthropicRootProperties(root, root, names, make(map[string]bool))
	props, _ := root["properties"].(map[string]any)
	if props == nil {
		props = make(map[string]any)
		root["properties"] = props
	}
	for name := range names {
		if _, exists := props[name]; !exists {
			props[name] = anthropicPropertyProjection(root, root, name, make(map[string]bool))
		}
	}
	for _, key := range []string{"oneOf", "allOf", "anyOf"} {
		delete(root, key)
	}
	// This keyword counts evaluations performed by the removed branches. Keeping
	// it could newly reject fields accepted through their patternProperties.
	delete(root, "unevaluatedProperties")
	root["type"] = "object"
}

func collectAnthropicRootProperties(value any, root map[string]any, names map[string]struct{}, refs map[string]bool) {
	node, ok := value.(map[string]any)
	if !ok {
		return
	}
	if properties, ok := node["properties"].(map[string]any); ok {
		for name := range properties {
			names[name] = struct{}{}
		}
	}
	if ref, ok := node["$ref"].(string); ok && !refs[ref] {
		refs[ref] = true
		collectAnthropicRootProperties(anthropicLocalSchemaRef(root, ref), root, names, refs)
		delete(refs, ref)
	}
	for _, keyword := range []string{"oneOf", "allOf", "anyOf"} {
		if branches, ok := node[keyword].([]any); ok {
			for _, branch := range branches {
				collectAnthropicRootProperties(branch, root, names, refs)
			}
		}
	}
}

func anthropicPropertyProjection(value any, root map[string]any, name string, refs map[string]bool) any {
	if boolean, ok := value.(bool); ok {
		if boolean {
			return map[string]any{}
		}
		return false
	}
	node, ok := value.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	if ref, ok := node["$ref"].(string); ok {
		if refs[ref] {
			return map[string]any{}
		}
		refs[ref] = true
		result := anthropicPropertyProjection(anthropicLocalSchemaRef(root, ref), root, name, refs)
		delete(refs, ref)
		// Ignoring siblings is conservative across the supported JSON Schema drafts.
		return result
	}
	constraint := any(map[string]any{})
	if props, ok := node["properties"].(map[string]any); ok {
		if property, exists := props[name]; exists {
			constraint = property
		} else if lenSchemaPatterns(node) == 0 {
			if additional, exists := node["additionalProperties"]; exists {
				constraint = additional
			}
		}
	} else if lenSchemaPatterns(node) == 0 {
		if additional, exists := node["additionalProperties"]; exists {
			constraint = additional
		}
	}
	constraints := []any{constraint}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf"} {
		branches, ok := node[keyword].([]any)
		if !ok || len(branches) == 0 {
			continue
		}
		projected := make([]any, 0, len(branches))
		for _, branch := range branches {
			projected = append(projected, anthropicPropertyProjection(branch, root, name, refs))
		}
		if keyword == "allOf" {
			constraints = append(constraints, projected...)
		} else {
			// Exclusivity involves the other fields, so nested oneOf would be too strict.
			constraints = append(constraints, map[string]any{"anyOf": projected})
		}
	}
	kept := make([]any, 0, len(constraints))
	for _, item := range constraints {
		if item == true {
			continue
		}
		if object, ok := item.(map[string]any); ok && len(object) == 0 {
			continue
		}
		kept = append(kept, item)
	}
	if len(kept) == 0 {
		return map[string]any{}
	}
	if len(kept) == 1 {
		return kept[0]
	}
	return map[string]any{"allOf": kept}
}

func lenSchemaPatterns(node map[string]any) int {
	patterns, _ := node["patternProperties"].(map[string]any)
	return len(patterns)
}

func anthropicLocalSchemaRef(root map[string]any, ref string) any {
	if ref == "#" {
		return root
	}
	if !strings.HasPrefix(ref, "#/") {
		return nil
	}
	var value any = root
	for _, encoded := range strings.Split(ref[2:], "/") {
		token := strings.ReplaceAll(strings.ReplaceAll(encoded, "~1", "/"), "~0", "~")
		switch node := value.(type) {
		case map[string]any:
			value = node[token]
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(node) {
				return nil
			}
			value = node[index]
		default:
			return nil
		}
	}
	return value
}

// Only references to removed root paths need relocation. Schema traversal avoids
// treating objects in const/default/enum as schemas and rewriting instance data.
func preserveAnthropicRootReferences(root map[string]any) {
	targets := make(map[string]string)
	walkAnthropicSchema(root, func(node map[string]any) {
		ref, _ := node["$ref"].(string)
		for _, keyword := range []string{"oneOf", "allOf", "anyOf"} {
			prefix := "#/" + keyword
			if (ref == prefix || strings.HasPrefix(ref, prefix+"/")) && root[keyword] != nil {
				targets[keyword] = ""
			}
		}
	})
	if len(targets) == 0 {
		return
	}
	defs, _ := root["$defs"].(map[string]any)
	if defs == nil {
		defs = make(map[string]any)
		root["$defs"] = defs
	}
	name := "supercli_root_branches"
	for suffix := 1; defs[name] != nil; suffix++ {
		name = "supercli_root_branches_" + strconv.Itoa(suffix)
	}
	saved := make(map[string]any)
	for keyword := range targets {
		saved[keyword] = root[keyword]
		targets[keyword] = "#/$defs/" + name + "/" + keyword
	}
	defs[name] = saved
	walkAnthropicSchema(root, func(node map[string]any) {
		ref, _ := node["$ref"].(string)
		for keyword, target := range targets {
			prefix := "#/" + keyword
			if ref == prefix || strings.HasPrefix(ref, prefix+"/") {
				node["$ref"] = target + strings.TrimPrefix(ref, prefix)
				break
			}
		}
	})
}

func walkAnthropicSchema(value any, visit func(map[string]any)) {
	node, ok := value.(map[string]any)
	if !ok {
		return
	}
	visit(node)
	for _, keyword := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"} {
		if children, ok := node[keyword].(map[string]any); ok {
			for _, child := range children {
				walkAnthropicSchema(child, visit)
			}
		}
	}
	for _, keyword := range []string{"additionalProperties", "unevaluatedProperties", "additionalItems", "unevaluatedItems", "not", "if", "then", "else", "contains", "propertyNames", "contentSchema"} {
		walkAnthropicSchema(node[keyword], visit)
	}
	if items, ok := node["items"].([]any); ok {
		for _, child := range items {
			walkAnthropicSchema(child, visit)
		}
	} else {
		walkAnthropicSchema(node["items"], visit)
	}
	for _, keyword := range []string{"anyOf", "oneOf", "allOf", "prefixItems"} {
		if children, ok := node[keyword].([]any); ok {
			for _, child := range children {
				walkAnthropicSchema(child, visit)
			}
		}
	}
}
