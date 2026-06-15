package workflow

import (
	"context"
	"encoding/json"
	"testing"

	"supercli/internal/storage/library"
)

func TestCheckLibraryAlternatives_Basic(t *testing.T) {
	finder := library.NewFinder()
	tool := NewCheckLibraryAlternatives(finder)

	args, _ := json.Marshal(checkLibraryArgs{
		Library: "leaflet",
		Task:    "10k polygons",
	})
	r, err := tool.execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if r.Text == "" {
		t.Fatal("expected non-empty text")
	}
	if !containsStr(r.Text, "MapLibre GL") {
		t.Errorf("text should mention MapLibre GL: %s", r.Text)
	}
}

func TestCheckLibraryAlternatives_NilFinder(t *testing.T) {
	tool := NewCheckLibraryAlternatives(nil)
	args, _ := json.Marshal(checkLibraryArgs{Library: "x", Task: "y"})
	r, err := tool.execute(context.Background(), args)
	if err != nil {
		t.Fatalf("nil finder should not error: %v", err)
	}
	if !containsStr(r.Text, "not configured") {
		t.Errorf("expected 'not configured' message: %s", r.Text)
	}
}

func TestCheckLibraryAlternatives_EmptyLibrary(t *testing.T) {
	tool := NewCheckLibraryAlternatives(library.NewFinder())
	args, _ := json.Marshal(checkLibraryArgs{Library: "", Task: "y"})
	r, err := tool.execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !containsStr(r.Text, "library name") {
		t.Errorf("expected library name hint: %s", r.Text)
	}
}

func TestCheckLibraryAlternatives_EmptyTask(t *testing.T) {
	tool := NewCheckLibraryAlternatives(library.NewFinder())
	args, _ := json.Marshal(checkLibraryArgs{Library: "leaflet", Task: ""})
	r, err := tool.execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !containsStr(r.Text, "use case") {
		t.Errorf("expected use case hint: %s", r.Text)
	}
}

func TestCheckLibraryAlternatives_BadJSON(t *testing.T) {
	tool := NewCheckLibraryAlternatives(library.NewFinder())
	r, err := tool.execute(context.Background(), []byte("not json"))
	if err != nil {
		t.Fatalf("bad JSON should not error: %v", err)
	}
	// Error goes into Result.Err (user-visible tool error),
	// not Result.Text. Check the Err field.
	if r.Err == nil {
		t.Fatal("expected Result.Err for bad JSON")
	}
	if !containsStr(r.Err.Error(), "bad args") {
		t.Errorf("expected 'bad args' in error: %s", r.Err.Error())
	}
}

func TestCheckLibraryAlternatives_NoMatch(t *testing.T) {
	tool := NewCheckLibraryAlternatives(library.NewFinder())
	args, _ := json.Marshal(checkLibraryArgs{Library: "totally-unknown-lib", Task: "anything"})
	r, err := tool.execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !containsStr(r.Text, "No known better alternative") {
		t.Errorf("expected 'no known' message: %s", r.Text)
	}
}

func TestCheckLibraryAlternatives_Spec(t *testing.T) {
	tool := NewCheckLibraryAlternatives(library.NewFinder())
	spec := tool.Spec()
	if spec.Name != "check_library_alternatives" {
		t.Errorf("Name = %q", spec.Name)
	}
	if spec.Description == "" {
		t.Error("empty Description")
	}
	if spec.Schema == "" {
		t.Error("empty Schema")
	}
	if spec.Fn == nil {
		t.Error("Fn is nil")
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
