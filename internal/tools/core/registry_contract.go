package core

import (
	"encoding/json"
	"fmt"
	"strings"
)

// validateContract checks the registration metadata that the registry and the
// approval gate rely on. A violation is a programming error in trusted tool
// code, so registration fails instead of running the tool with a weaker
// contract than it declares.
func (t Tool) validateContract() error {
	schema := strings.TrimSpace(t.Schema)
	if schema == "" {
		if !t.UncheckedArgs {
			return fmt.Errorf("tool %q: Schema is empty; declare an argument schema or set UncheckedArgs explicitly", t.Name)
		}
	} else if t.UncheckedArgs {
		return fmt.Errorf("tool %q: UncheckedArgs is set but a Schema is declared", t.Name)
	}
	if t.ApprovalArg != "" && t.Approval == nil {
		return fmt.Errorf("tool %q: ApprovalArg %q without Approval: the host would never own the confirmation", t.Name, t.ApprovalArg)
	}
	if t.ReadOnly && t.Approval != nil {
		return fmt.Errorf("tool %q: ReadOnly tools cannot require approval", t.Name)
	}
	for _, field := range []struct{ kind, name string }{{"ApprovalArg", t.ApprovalArg}, {"RefreshArg", t.RefreshArg}} {
		if field.name == "" {
			continue
		}
		if !schemaDeclaresBoolean(schema, field.name) {
			return fmt.Errorf("tool %q: %s %q is not a boolean property of the schema", t.Name, field.kind, field.name)
		}
	}
	return nil
}

// schemaDeclaresBoolean reports whether the root object schema declares name
// as a boolean property.
func schemaDeclaresBoolean(schema, name string) bool {
	if schema == "" {
		return false
	}
	var root struct {
		Properties map[string]struct {
			Type any `json:"type"`
		} `json:"properties"`
	}
	if json.Unmarshal([]byte(schema), &root) != nil {
		return false
	}
	prop, ok := root.Properties[name]
	if !ok {
		return false
	}
	switch typ := prop.Type.(type) {
	case string:
		return typ == "boolean"
	case []any:
		for _, item := range typ {
			if item == "boolean" {
				return true
			}
		}
	}
	return false
}
