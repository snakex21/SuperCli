package tools

import (
	"encoding/json"
	"sort"
	"strings"
)

// catalog.go implements F-thin step B1: a compact, non-JSON
// rendering of the tool set for the system prompt.
//
// The full JSON Schema of every visible tool is the dominant
// per-turn input cost (measured baseline ~2,500 tokens for the
// always-on core). For the long tail of tools the model rarely
// needs the schema up front — it needs to know the tool EXISTS
// and roughly how to use it, then pull the full schema on demand.
//
// RenderCatalog produces one line per tool:
//
//	name — first line of description
//
// This is the "lekki katalog zamiast pełnych schematów" from the
// roadmap (TODO chudy protokół, pkt 1). It does not change any
// request path on its own; it is the building block the loop will
// use to replace bulk schema emission for the tool tail.

// CatalogEntry is one tool reduced to its advertisement: the
// compact call signature plus a single-line usage hint. It carries
// no JSON Schema, but the signature lists the argument names, their
// types, and which are required so a small model can form a valid
// call on the first try (the sparse name-only catalog made local
// models guess args and fail-loop).
type CatalogEntry struct {
	// Name is the bare tool name (kept for callers that need it).
	Name string
	// Sig is the compact call signature, e.g.
	// "tool_search(query, limit:int=10)". Required args appear
	// plain; optional args carry ":type" and "=default". Falls back
	// to just the name when the schema has no usable properties.
	Sig  string
	Hint string
}

// firstLine returns the first non-empty line of s, trimmed and
// collapsed to a single line. Newlines inside a description would
// break the one-line-per-tool catalog format, so we take only the
// leading sentence-ish chunk.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// truncateHint caps a hint at max runes, appending an ellipsis
// when it had to cut. max <= 0 disables truncation. Operates on
// runes so multibyte (UTF-8) content is never split mid-character.
func truncateHint(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

// CatalogEntries reduces tools to their name+hint advertisements,
// sorted by name for a stable, deterministic catalog (stable order
// is what lets a provider prompt-cache the prefix). hintMax caps
// each hint's length in runes; <= 0 means no cap.
func CatalogEntries(tools []Tool, hintMax int) []CatalogEntry {
	out := make([]CatalogEntry, 0, len(tools))
	for _, t := range tools {
		out = append(out, CatalogEntry{
			Name: t.Name,
			Sig:  toolSignature(t.Name, t.Schema),
			Hint: truncateHint(firstLine(t.Description), hintMax),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RenderCatalog renders tools as a compact catalog block: one
// "name — hint" line per tool, sorted by name. hintMax caps each
// hint in runes (<= 0 = no cap). Returns "" for an empty tool set
// so callers can omit the block entirely rather than emit a stray
// header.
func RenderCatalog(tools []Tool, hintMax int) string {
	entries := CatalogEntries(tools, hintMax)
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e.Sig)
		if e.Hint != "" {
			b.WriteString(" — ")
			b.WriteString(e.Hint)
		}
	}
	return b.String()
}

// jsonTypeAbbrev maps a JSON-Schema type to the compact token shown
// in a signature. Strings carry no ":type" suffix (they are the common
// case and the name usually implies it); everything else gets a short
// tag so the model knows not to quote a number/bool.
func jsonTypeAbbrev(t string) string {
	switch t {
	case "integer":
		return "int"
	case "number":
		return "num"
	case "boolean":
		return "bool"
	case "array":
		return "list"
	case "object":
		return "obj"
	default: // "string" and anything unknown
		return ""
	}
}

// toolSignature renders a compact call signature for one tool from its
// JSON Schema, e.g. tool_search(query, limit:int=10) or task(agent,
// prompt). Required args appear first and bare; optional args follow,
// carrying ":type" (non-string only) and "=default" when the schema
// declares one. Property order from the schema is preserved so the
// signature reads the way the schema author intended.
//
// It is derived entirely from the tool's existing schema — no
// hand-maintained arg list — so it can never drift from the real
// arguments. On any parse problem it degrades to just the bare name,
// which is exactly the old name-only catalog behaviour.
func toolSignature(name, schema string) string {
	props, order, required := parseSchemaProps(schema)
	if len(order) == 0 {
		return name
	}
	req := make(map[string]bool, len(required))
	for _, r := range required {
		req[r] = true
	}

	// Required args first (in schema order), then optional args.
	var reqArgs, optArgs []string
	for _, key := range order {
		p := props[key]
		if req[key] {
			reqArgs = append(reqArgs, key)
			continue
		}
		seg := key
		if abbr := jsonTypeAbbrev(p.typ); abbr != "" {
			seg += ":" + abbr
		}
		if p.def != "" {
			seg += "=" + p.def
		}
		optArgs = append(optArgs, seg)
	}

	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('(')
	b.WriteString(strings.Join(append(reqArgs, optArgs...), ", "))
	b.WriteByte(')')
	return b.String()
}

// schemaProp is one parsed property: its declared type (may be "") and
// its default rendered as a compact literal (may be "").
type schemaProp struct {
	typ string
	def string
}

// parseSchemaProps extracts the top-level properties of a JSON-Schema
// object, preserving their declaration order, along with the required
// list. Order is preserved by streaming the "properties" object with a
// json.Decoder (a plain map would randomise it). Returns empty results
// on any parse failure or when there are no properties, so the caller
// falls back to the bare name.
func parseSchemaProps(schema string) (props map[string]schemaProp, order []string, required []string) {
	if strings.TrimSpace(schema) == "" {
		return nil, nil, nil
	}
	// First pull the required[] list and the raw properties object via a
	// normal unmarshal (order of required does not matter).
	var top struct {
		Required   []string        `json:"required"`
		Properties json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(schema), &top); err != nil {
		return nil, nil, nil
	}
	if len(top.Properties) == 0 {
		return nil, nil, nil
	}

	props = make(map[string]schemaProp)
	dec := json.NewDecoder(strings.NewReader(string(top.Properties)))
	// Expect an opening '{'.
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, nil, nil
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, nil, nil
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, nil, nil
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, nil, nil
		}
		props[key] = parseOneProp(raw)
		order = append(order, key)
	}
	return props, order, top.Required
}

// parseOneProp reads a single property schema and returns its type and
// a compact default literal. Unknown/absent fields yield "".
func parseOneProp(raw json.RawMessage) schemaProp {
	var p struct {
		Type    json.RawMessage `json:"type"`
		Default json.RawMessage `json:"default"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return schemaProp{}
	}
	var sp schemaProp
	// Type may be a string or a union ([]string); only a plain string
	// type is rendered.
	var t string
	if json.Unmarshal(p.Type, &t) == nil {
		sp.typ = t
	}
	if len(p.Default) > 0 {
		sp.def = compactDefault(p.Default)
	}
	return sp
}

// compactDefault renders a schema default as a short signature literal:
// strings lose their quotes, scalars pass through, and anything bulky
// (objects/arrays) is dropped to keep the signature tight.
func compactDefault(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	switch s[0] {
	case '{', '[':
		return "" // too bulky for a one-line signature
	case '"':
		var str string
		if json.Unmarshal(raw, &str) == nil {
			return str
		}
		return ""
	default:
		return s // number / bool literal
	}
}

// EstimateSchemaTokens approximates the input-token cost of
// emitting the full JSON Schema of every tool (name + description
// + schema), the way providers serialise ToolDef today. The
// estimate uses the project-wide chars/4 heuristic (same as
// agent.EstimateVisibleTokens) so catalog savings can be compared
// against the schema baseline without a tokenizer dependency.
func EstimateSchemaTokens(tools []Tool) int {
	chars := 0
	for _, t := range tools {
		chars += len(t.Name) + len(t.Description) + len(t.Schema)
	}
	return chars / 4
}

// EstimateCatalogTokens approximates the input-token cost of the
// compact catalog rendering, using the same chars/4 heuristic as
// EstimateSchemaTokens for an apples-to-apples comparison.
func EstimateCatalogTokens(tools []Tool, hintMax int) int {
	return len(RenderCatalog(tools, hintMax)) / 4
}
