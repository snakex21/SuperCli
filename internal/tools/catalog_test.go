package tools

import (
	"strings"
	"testing"
)

func catalogToolFixtures() []Tool {
	return []Tool{
		{Name: "zebra", Description: "Last alphabetically.", Schema: `{"type":"object"}`},
		{Name: "alpha", Description: "First tool.\nSecond line ignored.", Schema: `{"type":"object","properties":{"x":{"type":"string"}}}`},
		{Name: "mid", Description: "  Middle tool with padding.  ", Schema: `{}`},
	}
}

// --- RenderCatalog: happy path ---

func TestRenderCatalog_SortedOneLinePerTool(t *testing.T) {
	got := RenderCatalog(catalogToolFixtures(), 0)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d: %q", len(lines), got)
	}
	// Sorted by name: alpha, mid, zebra. Each line now leads with the
	// compact call signature (alpha has one optional string prop "x";
	// mid/zebra have no usable props so fall back to the bare name).
	if !strings.HasPrefix(lines[0], "alpha(x) — First tool.") {
		t.Errorf("line 0 = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "mid — Middle tool with padding.") {
		t.Errorf("line 1 = %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "zebra — Last alphabetically.") {
		t.Errorf("line 2 = %q", lines[2])
	}
}

func TestRenderCatalog_FirstLineOnly(t *testing.T) {
	got := RenderCatalog(catalogToolFixtures(), 0)
	if strings.Contains(got, "Second line ignored") {
		t.Errorf("multiline description leaked into catalog: %q", got)
	}
}

// --- RenderCatalog: edge cases ---

func TestRenderCatalog_Empty(t *testing.T) {
	if got := RenderCatalog(nil, 0); got != "" {
		t.Errorf("empty tools = %q, want empty string", got)
	}
	if got := RenderCatalog([]Tool{}, 0); got != "" {
		t.Errorf("empty slice = %q, want empty string", got)
	}
}

func TestRenderCatalog_NoDescriptionOmitsDash(t *testing.T) {
	got := RenderCatalog([]Tool{{Name: "bare", Description: "", Schema: "{}"}}, 0)
	if got != "bare" {
		t.Errorf("bare tool = %q, want %q", got, "bare")
	}
}

func TestTruncateHint_RuneSafe(t *testing.T) {
	// Multibyte content must not be split mid-character.
	in := "zażółć gęślą jaźń" // Polish, multibyte
	out := truncateHint(in, 6)
	if !strings.HasSuffix(out, "…") {
		t.Errorf("want ellipsis suffix, got %q", out)
	}
	// Must remain valid UTF-8 (range over runes never sees RuneError
	// from a split byte if we truncated on a rune boundary).
	for i, r := range out {
		if r == '\uFFFD' {
			t.Errorf("invalid rune at byte %d in %q", i, out)
		}
	}
	if got := []rune(out); len(got) != 6 {
		t.Errorf("want 6 runes, got %d (%q)", len(got), out)
	}
}

func TestTruncateHint_NoCapWhenZero(t *testing.T) {
	in := "this is a long hint that should not be touched"
	if got := truncateHint(in, 0); got != in {
		t.Errorf("max=0 should not truncate; got %q", got)
	}
	if got := truncateHint(in, -5); got != in {
		t.Errorf("negative max should not truncate; got %q", got)
	}
}

func TestTruncateHint_ShorterThanCap(t *testing.T) {
	in := "short"
	if got := truncateHint(in, 100); got != in {
		t.Errorf("short hint changed: %q", got)
	}
}

func TestRenderCatalog_HintCapApplied(t *testing.T) {
	tools := []Tool{{Name: "t", Description: "a very long description that exceeds the cap", Schema: "{}"}}
	got := RenderCatalog(tools, 10)
	hint := strings.TrimPrefix(got, "t — ")
	if r := []rune(hint); len(r) != 10 {
		t.Errorf("hint not capped to 10 runes: %q (%d)", hint, len(r))
	}
}

func TestFirstLine_EmptyAndWhitespace(t *testing.T) {
	if got := firstLine(""); got != "" {
		t.Errorf("empty = %q", got)
	}
	if got := firstLine("   \n  "); got != "" {
		t.Errorf("whitespace-only = %q", got)
	}
	if got := firstLine("  hello  \nworld"); got != "hello" {
		t.Errorf("got %q, want hello", got)
	}
}

// --- toolSignature ---

func TestToolSignature_RequiredFirstAndTypedOptional(t *testing.T) {
	// Mirrors the real tool_search schema: required "query" (string),
	// optional "limit" (integer, default 10). Want: leading bare
	// required arg, then optional carrying :int and =default.
	schema := `{
		"type":"object",
		"properties":{
			"query":{"type":"string"},
			"limit":{"type":"integer","default":10}
		},
		"required":["query"]
	}`
	if got := toolSignature("tool_search", schema); got != "tool_search(query, limit:int=10)" {
		t.Errorf("got %q, want %q", got, "tool_search(query, limit:int=10)")
	}
}

func TestToolSignature_MultipleRequiredNoTypeTagForStrings(t *testing.T) {
	// task(agent, prompt): both required strings -> bare names, no
	// :type tag (string is the unmarked default).
	schema := `{
		"type":"object",
		"properties":{
			"agent":{"type":"string"},
			"prompt":{"type":"string"}
		},
		"required":["agent","prompt"]
	}`
	if got := toolSignature("task", schema); got != "task(agent, prompt)" {
		t.Errorf("got %q, want %q", got, "task(agent, prompt)")
	}
}

func TestToolSignature_PreservesPropertyOrder(t *testing.T) {
	// Declaration order (b, a, c) must be preserved, not sorted.
	schema := `{"type":"object","properties":{
		"b":{"type":"string"},
		"a":{"type":"string"},
		"c":{"type":"string"}
	}}`
	if got := toolSignature("t", schema); got != "t(b, a, c)" {
		t.Errorf("got %q, want %q (order must be preserved)", got, "t(b, a, c)")
	}
}

func TestToolSignature_BoolAndNumberTags(t *testing.T) {
	schema := `{"type":"object","properties":{
		"async":{"type":"boolean","default":false},
		"ratio":{"type":"number"}
	}}`
	if got := toolSignature("t", schema); got != "t(async:bool=false, ratio:num)" {
		t.Errorf("got %q, want %q", got, "t(async:bool=false, ratio:num)")
	}
}

func TestToolSignature_FallbackToBareName(t *testing.T) {
	cases := []string{"", "{}", `{"type":"object"}`, "not json", `{"type":"object","properties":{}}`}
	for _, schema := range cases {
		if got := toolSignature("bare", schema); got != "bare" {
			t.Errorf("schema %q: got %q, want bare name", schema, got)
		}
	}
}

func TestToolSignature_StringDefaultUnquoted(t *testing.T) {
	schema := `{"type":"object","properties":{
		"mode":{"type":"string","default":"fast"}
	}}`
	if got := toolSignature("t", schema); got != "t(mode=fast)" {
		t.Errorf("got %q, want %q", got, "t(mode=fast)")
	}
}

// --- token estimators ---

func TestEstimateCatalogBeatsSchema(t *testing.T) {
	tools := catalogToolFixtures()
	schemaTok := EstimateSchemaTokens(tools)
	catalogTok := EstimateCatalogTokens(tools, 0)
	if catalogTok >= schemaTok {
		t.Errorf("catalog (%d tok) should be cheaper than schema (%d tok)", catalogTok, schemaTok)
	}
}

func TestEstimateSchemaTokens_CountsAllFields(t *testing.T) {
	tools := []Tool{{Name: "abcd", Description: "efgh", Schema: "ijkl"}}
	// 4+4+4 = 12 chars / 4 = 3 tokens.
	if got := EstimateSchemaTokens(tools); got != 3 {
		t.Errorf("got %d tokens, want 3", got)
	}
}

func TestEstimateCatalogTokens_Empty(t *testing.T) {
	if got := EstimateCatalogTokens(nil, 0); got != 0 {
		t.Errorf("empty = %d, want 0", got)
	}
}

func TestCatalogEntries_StableSort(t *testing.T) {
	entries := CatalogEntries(catalogToolFixtures(), 0)
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}
	want := []string{"alpha", "mid", "zebra"}
	for i, e := range entries {
		if e.Name != want[i] {
			t.Errorf("entry %d = %q, want %q", i, e.Name, want[i])
		}
	}
}
