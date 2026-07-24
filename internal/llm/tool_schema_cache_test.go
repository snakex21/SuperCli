package llm

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestNormalizeToolSchemaByteStable is a KV-cache guard. Chat
// templates serialize tool schemas near the START of the prompt, so
// the bytes must be identical every turn or the whole cached prefix
// is invalidated on the slow local backend. normalizeToolSchema
// round-trips through map[string]any; this pins that the re-marshal
// is deterministic (Go sorts map keys) across many-keyed, nested
// schemas and repeated calls.
func TestNormalizeToolSchemaByteStable(t *testing.T) {
	raw := `{"type":"object","properties":{"zeta":{"type":"string"},"alpha":{"type":"integer"},"mid":{"type":"boolean"},"nested":{"type":"object","properties":{"y":{"type":"string"},"x":{"type":"string"}}}},"required":["zeta","alpha"]}`

	first := string(normalizeToolSchema(raw))
	for i := 0; i < 50; i++ {
		if got := string(normalizeToolSchema(raw)); got != first {
			t.Fatalf("normalizeToolSchema not byte-stable on call %d:\nwant %q\ngot  %q", i, first, got)
		}
	}

	// Sanity: keys are emitted in sorted order (the reason it is
	// stable regardless of Go's randomized map iteration).
	if strings.Index(first, `"alpha"`) > strings.Index(first, `"zeta"`) {
		t.Errorf("top-level keys not sorted; alpha should precede zeta: %s", first)
	}
	if strings.Index(first, `"x"`) > strings.Index(first, `"y"`) {
		t.Errorf("nested keys not sorted; x should precede y: %s", first)
	}
}

func TestToolSchemaCacheIsBoundedAndReturnsCopies(t *testing.T) {
	normalizedToolSchemaCache.Lock()
	normalizedToolSchemaCache.entries = make(map[toolSchemaCacheKey]json.RawMessage)
	normalizedToolSchemaCache.order = nil
	normalizedToolSchemaCache.Unlock()

	first, err := normalizeOpenAIToolSchemaChecked(`{"type":"object","properties":{"x":{"type":"string"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	first[0] = '['
	again, err := normalizeOpenAIToolSchemaChecked(`{"type":"object","properties":{"x":{"type":"string"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if again[0] != '{' {
		t.Fatal("caller mutation corrupted cached schema")
	}

	for i := 0; i < toolSchemaCacheLimit+100; i++ {
		raw := fmt.Sprintf(`{"type":"object","properties":{"x%d":{"type":"string"}}}`, i)
		if _, err := normalizeOpenAIToolSchemaChecked(raw); err != nil {
			t.Fatal(err)
		}
	}
	normalizedToolSchemaCache.Lock()
	defer normalizedToolSchemaCache.Unlock()
	if got := len(normalizedToolSchemaCache.entries); got > toolSchemaCacheLimit {
		t.Fatalf("cache entries = %d, limit = %d", got, toolSchemaCacheLimit)
	}
}

func TestNormalizeOpenAIToolSchemaByteStable(t *testing.T) {
	raw := `{"type":"object","properties":{"choice":{"type":"object","anyOf":[{"required":["left"]},{"required":["right"]}]},"items":{"type":"array","items":{"properties":{"value":{"type":"string"}}}}},"anyOf":[{"required":["choice"]},{"required":["items"]}]}`

	first := string(normalizeOpenAIToolSchema(raw))
	for i := 0; i < 50; i++ {
		if got := string(normalizeOpenAIToolSchema(raw)); got != first {
			t.Fatalf("normalizeOpenAIToolSchema not byte-stable on call %d:\nwant %q\ngot  %q", i, first, got)
		}
	}
}
