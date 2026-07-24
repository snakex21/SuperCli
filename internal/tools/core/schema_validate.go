package core

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
)

// maxJSONNumberExponent prevents hostile/model-generated scientific notation
// from asking math/big to allocate an effectively unbounded power of ten.
// Ten thousand is far beyond useful tool arguments while keeping validation
// work and memory strictly bounded.
const maxJSONNumberExponent = 10_000

// compiledToolSchema is the local execution form of a tool's JSON Schema.
// It is built exactly once by Registry.Register and never mutated afterwards,
// so concurrent Execute calls only walk immutable Go values.
type compiledToolSchema struct {
	root *schemaNode
}

type schemaNode struct {
	boolean *bool
	types   map[string]struct{}

	properties     map[string]*schemaNode
	propertyOrder  []string
	required       []string
	additional     *schemaNode
	denyAdditional bool
	// patternProperties is not part of the deliberately small validator. If it
	// is present, unknown properties must remain allowed (fail-open), otherwise
	// additionalProperties:false could reject a property a pattern permits.
	hasPatternProperties bool

	items     *schemaNode
	prefix    []*schemaNode
	denyItems bool
	minItems  *int
	maxItems  *int
	unique    bool

	minLength *int
	maxLength *int
	pattern   *regexp.Regexp

	minimum *big.Rat
	maximum *big.Rat

	enumKeys map[string]struct{}
	hasConst bool
	constKey string

	anyOf []*schemaNode
	oneOf []*schemaNode
	allOf []*schemaNode
}

var schemaTypes = map[string]struct{}{
	"object": {}, "array": {}, "string": {}, "number": {},
	"integer": {}, "boolean": {}, "null": {},
}

// compileToolSchema normalizes both full JSON Schema and SuperCli's
// historical root-properties shorthand. An empty string deliberately has no
// validator (compatibility fail-open); {} is the normal unconstrained object
// schema used by tool APIs.
func compileToolSchema(raw string) (*compiledToolSchema, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	value, err := decodeJSONAny([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid JSON schema: %w", err)
	}
	switch root := value.(type) {
	case bool:
		node, err := compileSchemaNode(root, "$schema")
		if err != nil {
			return nil, err
		}
		return &compiledToolSchema{root: node}, nil
	case map[string]any:
		root = normalizeToolSchemaRoot(root)
		node, err := compileSchemaNode(root, "$schema")
		if err != nil {
			return nil, err
		}
		return &compiledToolSchema{root: node}, nil
	default:
		return nil, fmt.Errorf("invalid JSON schema: root must be an object or boolean")
	}
}

func normalizeToolSchemaRoot(root map[string]any) map[string]any {
	if len(root) == 0 {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if !looksLikeFullSchema(root) {
		return map[string]any{"type": "object", "properties": root}
	}
	// Tool arguments are object envelopes. This mirrors the provider-side
	// normalization while leaving explicitly typed full schemas untouched.
	if typ, exists := root["type"]; !exists || typ == nil {
		root["type"] = "object"
	}
	return root
}

func looksLikeFullSchema(root map[string]any) bool {
	for _, key := range []string{
		"$schema", "$id", "$ref", "$defs", "definitions",
		"type", "properties", "required", "additionalProperties",
		"patternProperties", "propertyNames", "dependentRequired", "dependentSchemas",
		"items", "prefixItems", "additionalItems", "contains",
		"minItems", "maxItems", "uniqueItems",
		"minLength", "maxLength", "pattern", "format",
		"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
		"enum", "const", "anyOf", "oneOf", "allOf", "not", "if", "then", "else",
		"title", "description", "default", "examples", "readOnly", "writeOnly",
	} {
		if _, ok := root[key]; ok {
			return true
		}
	}
	return false
}

func compileSchemaNode(value any, at string) (*schemaNode, error) {
	if boolean, ok := value.(bool); ok {
		v := boolean
		return &schemaNode{boolean: &v}, nil
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid JSON schema at %s: schema must be an object or boolean", at)
	}
	n := &schemaNode{}

	if raw, exists := obj["type"]; exists {
		types, err := compileTypes(raw, at+".type")
		if err != nil {
			return nil, err
		}
		n.types = types
	}
	if raw, exists := obj["properties"]; exists {
		props, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid JSON schema at %s.properties: must be an object", at)
		}
		n.properties = make(map[string]*schemaNode, len(props))
		n.propertyOrder = make([]string, 0, len(props))
		for name, child := range props {
			compiled, err := compileSchemaNode(child, at+".properties."+name)
			if err != nil {
				return nil, err
			}
			n.properties[name] = compiled
			n.propertyOrder = append(n.propertyOrder, name)
		}
		sort.Strings(n.propertyOrder)
	}
	if raw, exists := obj["required"]; exists {
		values, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("invalid JSON schema at %s.required: must be an array of strings", at)
		}
		seen := make(map[string]struct{}, len(values))
		for _, value := range values {
			name, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("invalid JSON schema at %s.required: must contain only strings", at)
			}
			if _, duplicate := seen[name]; duplicate {
				return nil, fmt.Errorf("invalid JSON schema at %s.required: duplicate %q", at, name)
			}
			seen[name] = struct{}{}
			n.required = append(n.required, name)
		}
	}
	if raw, exists := obj["additionalProperties"]; exists {
		switch value := raw.(type) {
		case bool:
			n.denyAdditional = !value
		case map[string]any:
			compiled, err := compileSchemaNode(value, at+".additionalProperties")
			if err != nil {
				return nil, err
			}
			n.additional = compiled
		default:
			return nil, fmt.Errorf("invalid JSON schema at %s.additionalProperties: must be a boolean or schema", at)
		}
	}
	_, n.hasPatternProperties = obj["patternProperties"]

	if raw, exists := obj["prefixItems"]; exists {
		prefix, err := compileSchemaArray(raw, at+".prefixItems")
		if err != nil {
			return nil, err
		}
		n.prefix = prefix
	}
	if raw, exists := obj["items"]; exists {
		switch value := raw.(type) {
		case bool:
			n.denyItems = !value
		case map[string]any:
			compiled, err := compileSchemaNode(value, at+".items")
			if err != nil {
				return nil, err
			}
			n.items = compiled
		case []any: // draft-07 tuple form
			if len(n.prefix) > 0 {
				return nil, fmt.Errorf("invalid JSON schema at %s.items: cannot combine tuple items with prefixItems", at)
			}
			prefix, err := compileSchemaArray(value, at+".items")
			if err != nil {
				return nil, err
			}
			n.prefix = prefix
		default:
			return nil, fmt.Errorf("invalid JSON schema at %s.items: must be a schema or schema array", at)
		}
	}
	if raw, exists := obj["additionalItems"]; exists && len(n.prefix) > 0 {
		switch value := raw.(type) {
		case bool:
			n.denyItems = !value
		case map[string]any:
			compiled, err := compileSchemaNode(value, at+".additionalItems")
			if err != nil {
				return nil, err
			}
			n.items = compiled
		default:
			return nil, fmt.Errorf("invalid JSON schema at %s.additionalItems: must be a boolean or schema", at)
		}
	}
	var err error
	if n.minItems, err = compileNonNegativeInt(obj, "minItems", at); err != nil {
		return nil, err
	}
	if n.maxItems, err = compileNonNegativeInt(obj, "maxItems", at); err != nil {
		return nil, err
	}
	if raw, exists := obj["uniqueItems"]; exists {
		unique, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("invalid JSON schema at %s.uniqueItems: must be a boolean", at)
		}
		n.unique = unique
	}
	if n.minLength, err = compileNonNegativeInt(obj, "minLength", at); err != nil {
		return nil, err
	}
	if n.maxLength, err = compileNonNegativeInt(obj, "maxLength", at); err != nil {
		return nil, err
	}
	if raw, exists := obj["pattern"]; exists {
		pattern, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("invalid JSON schema at %s.pattern: must be a string", at)
		}
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid JSON schema at %s.pattern: %w", at, err)
		}
		n.pattern = compiled
	}
	if n.minimum, err = compileNumber(obj, "minimum", at); err != nil {
		return nil, err
	}
	if n.maximum, err = compileNumber(obj, "maximum", at); err != nil {
		return nil, err
	}
	if raw, exists := obj["enum"]; exists {
		values, ok := raw.([]any)
		if !ok || len(values) == 0 {
			return nil, fmt.Errorf("invalid JSON schema at %s.enum: must be a non-empty array", at)
		}
		n.enumKeys = make(map[string]struct{}, len(values))
		for _, value := range values {
			key := canonicalValue(value)
			if _, duplicate := n.enumKeys[key]; duplicate {
				return nil, fmt.Errorf("invalid JSON schema at %s.enum: duplicate value", at)
			}
			n.enumKeys[key] = struct{}{}
		}
	}
	if value, exists := obj["const"]; exists {
		n.hasConst = true
		n.constKey = canonicalValue(value)
	}
	if n.anyOf, err = compileCombinator(obj, "anyOf", at); err != nil {
		return nil, err
	}
	if n.oneOf, err = compileCombinator(obj, "oneOf", at); err != nil {
		return nil, err
	}
	if n.allOf, err = compileCombinator(obj, "allOf", at); err != nil {
		return nil, err
	}
	return n, nil
}

func compileTypes(raw any, at string) (map[string]struct{}, error) {
	values := []any{raw}
	if union, ok := raw.([]any); ok {
		if len(union) == 0 {
			return nil, fmt.Errorf("invalid JSON schema at %s: type array cannot be empty", at)
		}
		values = union
	}
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		typ, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("invalid JSON schema at %s: must be a string or string array", at)
		}
		if _, valid := schemaTypes[typ]; !valid {
			return nil, fmt.Errorf("invalid JSON schema at %s: unsupported type %q", at, typ)
		}
		out[typ] = struct{}{}
	}
	return out, nil
}

func compileSchemaArray(raw any, at string) ([]*schemaNode, error) {
	values, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("invalid JSON schema at %s: must be an array", at)
	}
	out := make([]*schemaNode, 0, len(values))
	for i, value := range values {
		node, err := compileSchemaNode(value, fmt.Sprintf("%s[%d]", at, i))
		if err != nil {
			return nil, err
		}
		out = append(out, node)
	}
	return out, nil
}

func compileCombinator(obj map[string]any, keyword, at string) ([]*schemaNode, error) {
	raw, exists := obj[keyword]
	if !exists {
		return nil, nil
	}
	values, ok := raw.([]any)
	if !ok || len(values) == 0 {
		return nil, fmt.Errorf("invalid JSON schema at %s.%s: must be a non-empty schema array", at, keyword)
	}
	return compileSchemaArray(values, at+"."+keyword)
}

func compileNonNegativeInt(obj map[string]any, keyword, at string) (*int, error) {
	raw, exists := obj[keyword]
	if !exists {
		return nil, nil
	}
	number, ok := raw.(json.Number)
	if !ok {
		return nil, fmt.Errorf("invalid JSON schema at %s.%s: must be a non-negative integer", at, keyword)
	}
	rational, err := parseJSONRat(number)
	if err != nil || rational.Sign() < 0 || !rational.IsInt() || !rational.Num().IsInt64() {
		return nil, fmt.Errorf("invalid JSON schema at %s.%s: must be a non-negative integer", at, keyword)
	}
	value64 := rational.Num().Int64()
	if int64(int(value64)) != value64 {
		return nil, fmt.Errorf("invalid JSON schema at %s.%s: integer is too large", at, keyword)
	}
	value := int(value64)
	return &value, nil
}

func compileNumber(obj map[string]any, keyword, at string) (*big.Rat, error) {
	raw, exists := obj[keyword]
	if !exists {
		return nil, nil
	}
	number, ok := raw.(json.Number)
	if !ok {
		return nil, fmt.Errorf("invalid JSON schema at %s.%s: must be a number", at, keyword)
	}
	value, err := parseJSONRat(number)
	if err != nil {
		return nil, fmt.Errorf("invalid JSON schema at %s.%s: must be a number", at, keyword)
	}
	return value, nil
}
