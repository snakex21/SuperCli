package agent

import (
	"fmt"
	"strings"

	"supercli/internal/llm"
)

// sentinel_toolcall.go implements the thin tool protocol's call
// syntax (B3): sentinel-delimited tool calls that cost far fewer
// tokens than JSON function calls and eliminate the whole class of
// truncated/unbalanced-JSON errors that toolcall_repair.go patches.
//
// The model never writes JSON. It writes a block delimited by the
// guillemet sentinels « » (U+00AB / U+00BB) — characters a model
// will not emit by accident in prose or code:
//
//	«tool_name»                      → no arguments        → {}
//	«tool_name
//	key: value
//	other_key: value»                → one JSON field per line
//
// The first line after « is the tool name. Each subsequent
// "key: value" line becomes one JSON field. The value runs to end
// of line (single-line values); it is JSON-encoded internally so
// the existing registry.Execute dispatch (which consumes
// json.RawMessage) is reused unchanged — that inner JSON never
// reaches the model, so it costs zero model tokens.
//
// This parser is the pure, isolated half of B3; wiring into the
// streaming consume() path is a separate step.

const (
	sentinelOpen  = "\u00AB" // «
	sentinelClose = "\u00BB" // »
)

// extractSentinelToolCalls scans text for the first complete
// «...» block. It returns the parsed tool calls and the text
// BEFORE the block (to be emitted as normal assistant prose).
//
// If no opening sentinel is present, it returns (nil, ""). If the
// block has opened but not yet closed (mid-stream), it also
// returns (nil, "") so the caller keeps buffering — identical
// streaming contract to extractXMLToolCalls.
func extractSentinelToolCalls(text string) ([]llm.ToolCall, string) {
	start := strings.Index(text, sentinelOpen)
	if start < 0 {
		return nil, ""
	}
	rest := text[start+len(sentinelOpen):]
	rel := strings.Index(rest, sentinelClose)
	if rel < 0 {
		return nil, "" // not yet complete (streaming)
	}
	before := text[:start]
	inner := rest[:rel]
	tcs := parseSentinelBlock(inner)
	if len(tcs) == 0 {
		// Malformed (e.g. empty name): leave the text untouched so
		// it is shown as prose rather than silently swallowed.
		return nil, ""
	}
	return tcs, before
}

// parseSentinelBlock parses the content BETWEEN the sentinels
// (the « and » already stripped). The first non-empty line is the
// tool name; each following "key: value" line is one argument.
// Returns nil for a malformed block (no name).
func parseSentinelBlock(inner string) []llm.ToolCall {
	lines := strings.Split(inner, "\n")

	// First non-empty line = tool name.
	name := ""
	bodyStart := 0
	for i, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			name = strings.TrimSpace(ln)
			bodyStart = i + 1
			break
		}
	}
	if name == "" {
		return nil
	}

	var pairs []string
	for _, ln := range lines[bodyStart:] {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		ci := strings.IndexByte(ln, ':')
		if ci < 0 {
			// A non-empty line without a colon is not a key:value
			// pair. Skip it rather than corrupt the args — the model
			// occasionally adds a stray word; we stay forgiving.
			continue
		}
		key := strings.TrimSpace(ln[:ci])
		val := strings.TrimSpace(ln[ci+1:])
		if key == "" {
			continue
		}
		pairs = append(pairs, fmt.Sprintf(`%s:%s`, jsonString(key), jsonString(val)))
	}

	args := "{" + strings.Join(pairs, ",") + "}"
	return []llm.ToolCall{{
		ID:        "sentinel_" + name,
		Name:      name,
		Arguments: args,
	}}
}
