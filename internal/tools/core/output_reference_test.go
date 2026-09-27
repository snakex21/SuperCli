package core

import (
	"strings"
	"testing"
)

func TestStoredOutputHandleRecognizesOnlyBoundedOuterEnvelope(t *testing.T) {
	store := NewOutputStore()
	full := strings.Repeat("log line\n", 2000)
	large := store.Compact("fixture", full)
	handle := StoredOutputHandle(large)
	if handle == "" {
		t.Fatal("large preview reference not recognized")
	}
	retained := store.ModelContent("fixture", Result{Text: "report", RetainedText: full})
	retainedHandle := StoredOutputHandle(retained)
	if retainedHandle == "" || retainedHandle == handle {
		t.Fatal("retained-text reference not recognized")
	}
	// A structured report may quote an earlier preview. The outer attachment
	// holds the complete report and must win over its nested header.
	nested := store.ModelContent("fixture", Result{Text: large, ModelPreview: large, RetainedText: full + "outer"})
	outer := StoredOutputHandle(nested)
	if outer == "" || outer == handle {
		t.Fatal("inner reference superseded the outer attachment")
	}
	for _, text := range []string{
		"ordinary log handle=" + handle,
		"quoted source:\n" + large + "\nend of quote",
		strings.Replace(large, handle, "out_../../private", 1),
		"",
		"[large tool output: nope bytes; preview follows; handle=" + handle + "]",
		"[large tool output: -1 bytes; preview follows; handle=" + handle + "]",
		"[large tool output: 10 bytes; preview follows; handle=out_abcdef]extra",
		"[stored tool output: 10 bytes; handle=" + handle + "; read_output {\"handle\":\"out_abcdef\"}; saved]",
		"[stored tool output: 10 bytes; handle=out_abcdef; read_output {\"handle\":\"out_abcdef\"}; " + strings.Repeat("x", 300) + "]",
	} {
		if got := StoredOutputHandle(text); got != "" {
			t.Fatalf("invented reference %q from %q", got, text)
		}
	}
}

func TestStoredOutputHandleSupportsLegacyReferences(t *testing.T) {
	if got := StoredOutputHandle("[large tool output: 9000 bytes; preview follows; handle=out_000001]\npreview"); got != "out_000001" {
		t.Fatalf("legacy reference = %q", got)
	}
}
