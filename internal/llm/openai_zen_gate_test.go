package llm

import "testing"

// TestEnsureOpenCodeZenGateToolDefs pins the free-tier chat gate: the nested
// bash+read pair is appended when missing, left alone when present, and never
// duplicated. Any openai-compatible free model uses this same pair — no
// per-model allowlist.
func TestEnsureOpenCodeZenGateToolDefs(t *testing.T) {
	// SuperCli's real tools: neither bash nor read → both injected.
	got := ensureOpenCodeZenGateToolDefs([]ToolDef{
		{Name: "web_lookup", Schema: `{"type":"object"}`},
		{Name: "recall", Schema: `{"type":"object"}`},
	})
	if len(got) != 4 {
		t.Fatalf("len=%d, want 4", len(got))
	}
	if got[2].Name != "bash" || got[3].Name != "read" {
		t.Fatalf("tail = %s,%s; want bash,read", got[2].Name, got[3].Name)
	}
	// Nested OpenAI function shape (not flat name-top-level).
	for _, want := range []struct{ name, schema string }{
		{"bash", `{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`},
		{"read", `{"type":"object","properties":{"filePath":{"type":"string"}},"required":["filePath"]}`},
	} {
		var found bool
		for _, tool := range got {
			if tool.Name == want.name {
				found = true
				if tool.Schema != want.schema {
					t.Fatalf("%s schema=%s, want %s", want.name, tool.Schema, want.schema)
				}
			}
		}
		if !found {
			t.Fatalf("missing gate tool %s", want.name)
		}
	}

	// Idempotent: second pass does not append duplicates.
	again := ensureOpenCodeZenGateToolDefs(got)
	if len(again) != len(got) {
		t.Fatalf("second pass len=%d, want %d", len(again), len(got))
	}

	// Partial pair (bash only) → only read is added.
	partial := ensureOpenCodeZenGateToolDefs([]ToolDef{{Name: "bash", Schema: `{"type":"object"}`}})
	if len(partial) != 2 || partial[1].Name != "read" {
		t.Fatalf("partial = %d tools tail=%s; want 2 tools ending read", len(partial), partial[len(partial)-1].Name)
	}

	// Full pair already present → untouched.
	full := []ToolDef{{Name: "read"}, {Name: "bash"}}
	if out := ensureOpenCodeZenGateToolDefs(full); len(out) != 2 || out[0].Name != "read" || out[1].Name != "bash" {
		t.Fatalf("full pair mutated: %+v", out)
	}
}
