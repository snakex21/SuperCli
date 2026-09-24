package core

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestStructuredModelPreviewRetainsEvidenceAndIsBounded(t *testing.T) {
	for _, retained := range []string{"", "full retained text"} {
		store := NewOutputStore()
		result := Result{Text: "UI text", RetainedText: retained, ModelPreview: strings.Repeat("section preview\n", 1000)}
		content := store.ModelContent("read_many", result)
		want := retained
		if want == "" {
			want = result.Text
		}
		if len(content) > ModelOutputPreviewBytes+200 || len(store.entries) != 1 || store.entries[onlyOutputHandle(t, store)].text != want {
			t.Fatal("invalid preview or evidence")
		}
		serialized, err := json.Marshal(result)
		if err != nil || strings.Contains(string(serialized), "section preview") || strings.Contains(string(serialized), "full retained text") {
			t.Fatal("private content leaked")
		}
	}
}

func TestStructuredPreviewCannotHideErrorsOrRecurse(t *testing.T) {
	store := NewOutputStore()
	result := Result{Text: "original", ModelPreview: "success preview"}
	var missing *OutputStore
	if missing.ModelContent("read_many", result) != "original" || store.ModelContent("read_output", result) != "original" {
		t.Fatal("fallback changed")
	}
	result.Err = errors.New("read failed")
	content := store.ModelContent("read_many", result)
	if !strings.Contains(content, "read failed") || strings.Contains(content, "success preview") || len(store.entries) != 0 {
		t.Fatal(content)
	}
}
