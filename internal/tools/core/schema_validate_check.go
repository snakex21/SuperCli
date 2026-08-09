package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

func (s *compiledToolSchema) validateJSON(raw json.RawMessage) error {
	value, err := decodeJSONAny(raw)
	if err != nil {
		return fmt.Errorf("$: invalid JSON: %s", compactError(err.Error()))
	}
	return s.root.validate(value, "$")
}

func (n *schemaNode) validate(value any, path string) error {
	if n == nil {
		return nil
	}
	if n.boolean != nil {
		if *n.boolean {
			return nil
		}
		return fmt.Errorf("%s: value is not allowed", path)
	}
	key := canonicalValue(value)
	if len(n.enumKeys) > 0 {
		if _, ok := n.enumKeys[key]; !ok {
			return fmt.Errorf("%s: value is not in enum", path)
		}
	}
	if n.hasConst && key != n.constKey {
		return fmt.Errorf("%s: value does not match const", path)
	}
	if len(n.types) > 0 && !n.acceptsType(value) {
		return fmt.Errorf("%s: expected %s, got %s", path, n.typeLabel(), valueType(value))
	}
	if len(n.anyOf) > 0 {
		matched := false
		for _, branch := range n.anyOf {
			if branch.validate(value, path) == nil {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s: must match at least one anyOf branch", path)
		}
	}
	if len(n.oneOf) > 0 {
		matches := 0
		for _, branch := range n.oneOf {
			if branch.validate(value, path) == nil {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s: must match exactly one oneOf branch (matched %d)", path, matches)
		}
	}
	for _, branch := range n.allOf {
		if err := branch.validate(value, path); err != nil {
			return err
		}
	}

	switch typed := value.(type) {
	case map[string]any:
		// Unknown keys are reported before missing required ones on purpose: a
		// misspelled argument is usually the required argument, and naming the
		// key the caller actually sent lets the next attempt be the right one.
		unknown := make([]string, 0)
		for name := range typed {
			if _, known := n.properties[name]; !known {
				unknown = append(unknown, name)
			}
		}
		sort.Strings(unknown)
		for _, name := range unknown {
			childPath := propertyPath(path, name)
			switch {
			case n.additional != nil:
				if err := n.additional.validate(typed[name], childPath); err != nil {
					return err
				}
			case n.denyAdditional && !n.hasPatternProperties:
				return &unknownArgumentError{
					path:  childPath,
					valid: n.propertyOrder,
					hint:  nearestArgument(name, n.propertyOrder),
				}
			default:
				// Fail-open on purpose. The key is unknown to this node, but the
				// node was never sealed: it is a nested envelope (invoke_tool.args,
				// mcp_bridge.arguments) whose keys belong to another tool. Only
				// argument roots are sealed, in sealToolSchemaRoot.
			}
		}
		for _, name := range n.required {
			if _, ok := typed[name]; !ok {
				return fmt.Errorf("%s: is required", propertyPath(path, name))
			}
		}
		for _, name := range n.propertyOrder {
			if childValue, ok := typed[name]; ok {
				if err := n.properties[name].validate(childValue, propertyPath(path, name)); err != nil {
					return err
				}
			}
		}
	case []any:
		if n.minItems != nil && len(typed) < *n.minItems {
			return fmt.Errorf("%s: needs at least %d items", path, *n.minItems)
		}
		if n.maxItems != nil && len(typed) > *n.maxItems {
			return fmt.Errorf("%s: allows at most %d items", path, *n.maxItems)
		}
		if n.unique {
			seen := make(map[string]int, len(typed))
			for i, item := range typed {
				itemKey := canonicalValue(item)
				if first, duplicate := seen[itemKey]; duplicate {
					return fmt.Errorf("%s[%d]: duplicates item %d", path, i, first)
				}
				seen[itemKey] = i
			}
		}
		for i, item := range typed {
			var child *schemaNode
			switch {
			case i < len(n.prefix):
				child = n.prefix[i]
			case n.denyItems:
				return fmt.Errorf("%s[%d]: additional item is not allowed", path, i)
			default:
				child = n.items
			}
			if child != nil {
				if err := child.validate(item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	case string:
		length := utf8.RuneCountInString(typed)
		if n.minLength != nil && length < *n.minLength {
			return fmt.Errorf("%s: length must be at least %d", path, *n.minLength)
		}
		if n.maxLength != nil && length > *n.maxLength {
			return fmt.Errorf("%s: length must be at most %d", path, *n.maxLength)
		}
		if n.pattern != nil && !n.pattern.MatchString(typed) {
			return fmt.Errorf("%s: does not match required pattern", path)
		}
	case json.Number:
		number, err := parseJSONRat(typed)
		if err != nil {
			return fmt.Errorf("%s: invalid number", path)
		}
		if n.minimum != nil && number.Cmp(n.minimum) < 0 {
			return fmt.Errorf("%s: must be at least %s", path, n.minimum.RatString())
		}
		if n.maximum != nil && number.Cmp(n.maximum) > 0 {
			return fmt.Errorf("%s: must be at most %s", path, n.maximum.RatString())
		}
	}
	return nil
}

func (n *schemaNode) acceptsType(value any) bool {
	actual := valueType(value)
	if _, ok := n.types[actual]; ok {
		return true
	}
	if actual == "number" {
		if _, ok := n.types["integer"]; ok {
			number, err := parseJSONRat(value.(json.Number))
			return err == nil && number.Denom().Cmp(big.NewInt(1)) == 0
		}
	}
	return false
}

func (n *schemaNode) typeLabel() string {
	types := make([]string, 0, len(n.types))
	for typ := range n.types {
		types = append(types, typ)
	}
	sort.Strings(types)
	return strings.Join(types, " or ")
}

func valueType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "boolean"
	default:
		return "unknown"
	}
}

func coerceCompiledArgs(schema *compiledToolSchema, args json.RawMessage) json.RawMessage {
	if schema == nil || schema.root == nil || len(args) == 0 || len(schema.root.properties) == 0 {
		return args
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(args, &object) != nil {
		return args
	}
	changed := false
	for name, property := range schema.root.properties {
		if len(property.types) != 1 {
			continue
		}
		var want string
		for typ := range property.types {
			want = typ
		}
		raw, ok := object[name]
		if !ok {
			continue
		}
		switch want {
		case "integer", "number", "boolean":
			var text string
			if json.Unmarshal(raw, &text) != nil {
				continue
			}
			if converted, ok := coerceScalar(text, want); ok {
				object[name] = converted
				changed = true
			}
		case "array":
			// Lenient coercion for local/small models that serialize a
			// whole argv array as a string ({"command": "[\"git\",\"status\"]"}).
			// Only for fields whose items are plain strings (ctx_execute
			// command/env_extra, future argv-like fields), and only when the
			// string actually parses as a JSON array — a bare shell string
			// like "git status" stays untouched and fails schema validation
			// with a clear message instead of being guessed at.
			if property.items == nil || len(property.items.types) != 1 {
				continue
			}
			var wantItem string
			for typ := range property.items.types {
				wantItem = typ
			}
			if wantItem != "string" {
				continue
			}
			var text string
			if json.Unmarshal(raw, &text) != nil {
				continue
			}
			var arr []string
			if err := json.Unmarshal([]byte(text), &arr); err != nil || arr == nil {
				continue
			}
			encoded, err := json.Marshal(arr)
			if err != nil {
				continue
			}
			object[name] = encoded
			changed = true
		}
	}
	if !changed {
		return args
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return args
	}
	return encoded
}

func decodeJSONAny(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func parseJSONRat(number json.Number) (*big.Rat, error) {
	text := strings.ToLower(number.String())
	sign := 1
	if strings.HasPrefix(text, "-") {
		sign = -1
		text = text[1:]
	} else if strings.HasPrefix(text, "+") {
		text = text[1:]
	}
	exponent := 0
	if i := strings.IndexByte(text, 'e'); i >= 0 {
		parsed, err := strconv.Atoi(text[i+1:])
		if err != nil {
			return nil, err
		}
		if parsed < -maxJSONNumberExponent || parsed > maxJSONNumberExponent {
			return nil, fmt.Errorf("number exponent is out of range")
		}
		exponent = parsed
		text = text[:i]
	}
	fracDigits := 0
	if i := strings.IndexByte(text, '.'); i >= 0 {
		fracDigits = len(text) - i - 1
		text = text[:i] + text[i+1:]
	}
	integer := new(big.Int)
	if _, ok := integer.SetString(text, 10); !ok {
		return nil, fmt.Errorf("invalid number")
	}
	if sign < 0 {
		integer.Neg(integer)
	}
	denominator := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(fracDigits)), nil)
	ratio := new(big.Rat).SetFrac(integer, denominator)
	if exponent != 0 {
		power := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(absInt(exponent))), nil)
		if exponent > 0 {
			ratio.Mul(ratio, new(big.Rat).SetInt(power))
		} else {
			ratio.Quo(ratio, new(big.Rat).SetInt(power))
		}
	}
	return ratio, nil
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func canonicalValue(value any) string {
	var out strings.Builder
	appendCanonical(&out, value)
	return out.String()
}

func appendCanonical(out *strings.Builder, value any) {
	switch typed := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		if typed {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case string:
		encoded, _ := json.Marshal(typed)
		out.Write(encoded)
	case json.Number:
		if number, err := parseJSONRat(typed); err == nil {
			out.WriteByte('n')
			out.WriteString(number.RatString())
		} else {
			out.WriteString(typed.String())
		}
	case []any:
		out.WriteByte('[')
		for i, item := range typed {
			if i > 0 {
				out.WriteByte(',')
			}
			appendCanonical(out, item)
		}
		out.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			encoded, _ := json.Marshal(key)
			out.Write(encoded)
			out.WriteByte(':')
			appendCanonical(out, typed[key])
		}
		out.WriteByte('}')
	default:
		encoded, _ := json.Marshal(typed)
		out.Write(encoded)
	}
}

func propertyPath(parent, name string) string {
	if name != "" {
		simple := true
		for i, r := range name {
			if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
				simple = false
				break
			}
		}
		if simple {
			return parent + "." + name
		}
	}
	encoded, _ := json.Marshal(name)
	return parent + "[" + string(encoded) + "]"
}

func compactError(message string) string {
	message = strings.TrimSpace(message)
	const limit = 180
	if len(message) <= limit {
		return message
	}
	return message[:limit] + "..."
}
