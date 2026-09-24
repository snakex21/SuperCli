package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRetainedTextHasOneHandleAndBoundedPreview(t *testing.T) {
	for _, failure := range []bool{false, true} {
		store := NewOutputStore()
		result := Result{Text: strings.Repeat("preview data\n", 1000), RetainedText: strings.Repeat("full evidence\n", 20000)}
		if failure {
			result.Err = SelfContainedErr(errors.New("command_failed exit=7"))
		}
		content := store.ModelContent("ctx_execute", result)
		if len(content) > 5000 || strings.Contains(content, "full evidence") || strings.Count(content, "handle=out_") != 1 {
			t.Fatalf("bad model preview: %d bytes", len(content))
		}
		if failure && !strings.Contains(content, "command_failed exit=7") {
			t.Fatal("exit status lost")
		}
		if len(store.entries) != 1 || store.entries[onlyOutputHandle(t, store)].text != result.RetainedText {
			t.Fatal("store saved preview or duplicated output")
		}
		raw := json.RawMessage(`{"handle":"` + onlyOutputHandle(t, store) + `","query":"full evidence"}`)
		read, err := store.ReadOutputTool().Fn(context.Background(), raw)
		if err != nil || read.Err != nil || !strings.Contains(read.Text, "full evidence") {
			t.Fatalf("cannot retrieve: %v %v", err, read.Err)
		}
		serialized, err := json.Marshal(Result{Text: "preview", RetainedText: result.RetainedText})
		if err != nil || strings.Contains(string(serialized), "full evidence") {
			t.Fatal("capture leaked into serialized result")
		}
	}
}

func TestRetainedTextFallbackAndReaderExemption(t *testing.T) {
	result := Result{Text: "preview", RetainedText: strings.Repeat("x", outputStoreBytes+1)}
	var missing *OutputStore
	if got := missing.ModelContent("ctx_execute", result); got != "preview" {
		t.Fatal(got)
	}
	store := NewOutputStore()
	if got := store.ModelContent("read_output", result); got != "preview" || len(store.entries) != 0 {
		t.Fatal("reader recursively retained output")
	}
	content := store.ModelContent("ctx_execute", result)
	if !strings.HasPrefix(content, "preview") || !strings.Contains(content, "not retained") || len(store.entries) != 0 {
		t.Fatal("oversize fallback dishonest")
	}
}
