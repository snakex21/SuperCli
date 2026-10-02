package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCompleteModelTextStaysInlineWithoutIO(t *testing.T) {
	backend := &memoryOutputPersistence{}
	ctx := WithOutputPersistence(context.Background(), backend)
	result := Result{Text: "src/example.go:8:return true\nsrc/example.go:9:return false", ModelText: "[hits grouped by file]\nsrc/example.go\n8:return true\n9:return false"}
	for _, store := range []*OutputStore{NewOutputStore(), nil} {
		if got := store.ModelContentContext(ctx, "search_code", result); got != result.ModelText {
			t.Fatalf("complete model text changed: %q", got)
		}
		if store != nil && len(store.entries) != 0 {
			t.Fatal("complete view unnecessarily retained")
		}
	}
	if backend.saves != 0 || backend.reads != 0 {
		t.Fatal("complete inline view incurred I/O")
	}
	body, err := json.Marshal(result)
	if err != nil || strings.Contains(string(body), "grouped") || !strings.Contains(string(body), "src/example.go:8:return true") {
		t.Fatalf("UI or transcript changed: %s (%v)", body, err)
	}
}

func TestCompleteModelTextCannotOverrideErrorsOrEvidence(t *testing.T) {
	cases := []struct {
		name, tool string
		result     Result
	}{
		{"failure", "search_code", Result{Text: "diagnostic", ModelText: "compact success", Err: errors.New("search failed")}},
		{"read_output", "read_output", Result{Text: "original", ModelText: "different"}},
		{"retained", "search_code", Result{Text: "preview", RetainedText: "full evidence", ModelText: "different"}},
		{"structured_preview", "search_code", Result{Text: "original", ModelPreview: "use this preview", ModelText: "different"}},
		{"large_model_text", "search_code", Result{Text: "small", ModelText: strings.Repeat("x", ModelOutputInlineBytes+1)}},
		{"large_original", "search_code", Result{Text: strings.Repeat("evidence ", 2000), ModelText: "different"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStore, wantStore := NewOutputStore(), NewOutputStore()
			without := tc.result
			without.ModelText = ""
			got, want := gotStore.ModelContent(tc.tool, tc.result), wantStore.ModelContent(tc.tool, without)
			// Stored handles are random; compare the content envelope and retained evidence.
			clean := func(text string) string {
				if i := strings.Index(text, "handle=out_"); i >= 0 {
					return text[:i]
				}
				return text
			}
			if clean(got) != clean(want) || len(gotStore.entries) != len(wantStore.entries) {
				t.Fatalf("fallback changed: %q vs %q", got, want)
			}
			if len(gotStore.entries) > 0 && gotStore.entries[onlyOutputHandle(t, gotStore)].text != wantStore.entries[onlyOutputHandle(t, wantStore)].text {
				t.Fatal("retained evidence changed")
			}
		})
	}
}
