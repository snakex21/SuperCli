package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools/core"
)

func TestPatchUnknownFieldAlsoReportsMissingOldWithoutWriting(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.txt")
	if err := os.WriteFile(path, []byte("first second"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := core.NewRegistry()
	reg.MustRegister(NewPatchFile(root).Spec())
	// Production shape: a valid first edit plus a replacement-only second edit.
	// Dropping new_ignore or inventing an old anchor must never apply either edit.
	raw := []byte("{\"path\":\"source.txt\",\"changes\":[{\"old\":\"first\",\"new\":\"ONE\"},{\"new\":\"TWO\",\"new_ignore\":\"\"}]}")
	result, err := reg.Execute(context.Background(), "patch_file", json.RawMessage(raw))
	if err != nil || result.Err == nil {
		t.Fatalf("err=%v result=%+v", err, result)
	}
	for _, want := range []string{"$.changes[1].new_ignore: unknown argument", "missing required: $.changes[1].old"} {
		if !strings.Contains(result.Err.Error(), want) {
			t.Fatalf("error %q must report %q in the same response", result.Err, want)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "first second" {
		t.Fatalf("invalid call wrote content: %q, %v", data, err)
	}
	// The subsequent complete call should succeed, retaining atomic patch semantics.
	fixed := []byte("{\"path\":\"source.txt\",\"changes\":[{\"old\":\"first\",\"new\":\"ONE\"},{\"old\":\"second\",\"new\":\"TWO\"}]}")
	result, err = reg.Execute(context.Background(), "patch_file", json.RawMessage(fixed))
	if err != nil || result.Err != nil {
		t.Fatalf("corrected call: %v, %v", err, result.Err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "ONE TWO" {
		t.Fatalf("corrected call wrote %q", data)
	}
}
