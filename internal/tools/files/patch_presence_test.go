package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"supercli/internal/tools/core"
	"testing"
)

// Missing new is a malformed edit, not an instruction to delete. Test both
// registry dispatch and the handler; an invalid later change is atomic too.
func TestPatchRejectsIncompleteReplacementWithoutWriting(t *testing.T) {
	for _, viaRegistry := range []bool{false, true} {
		for _, tc := range []struct{ name, body string }{
			{name: "short form missing new", body: "{\"path\":\"a.txt\",\"old\":\"alpha\"}"},
			{name: "short form null new", body: "{\"path\":\"a.txt\",\"old\":\"alpha\",\"new\":null}"},
			{name: "batch missing new", body: "{\"path\":\"a.txt\",\"changes\":[{\"old\":\"alpha\",\"new\":\"ALPHA\"},{\"old\":\"beta\"}]}"},
			{name: "batch null new", body: "{\"path\":\"a.txt\",\"changes\":[{\"old\":\"alpha\",\"new\":\"ALPHA\"},{\"old\":\"beta\",\"new\":null}]}"},
			{name: "mixed forms with empty new", body: "{\"path\":\"a.txt\",\"new\":\"\",\"changes\":[{\"old\":\"alpha\",\"new\":\"ALPHA\"}]}"},
			{name: "mixed forms with empty old", body: "{\"path\":\"a.txt\",\"old\":\"\",\"changes\":[{\"old\":\"alpha\",\"new\":\"ALPHA\"}]}"},
		} {
			t.Run(tc.name+map[bool]string{false: "/handler", true: "/registry"}[viaRegistry], func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, "a.txt")
				const original = "alpha beta\n"
				if err := os.WriteFile(path, []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
				spec := NewPatchFile(root).Spec()
				var result core.Result
				var err error
				if viaRegistry {
					reg := core.NewRegistry()
					reg.MustRegister(spec)
					result, err = reg.Execute(context.Background(), spec.Name, json.RawMessage(tc.body))
				} else {
					result, err = spec.Fn(context.Background(), json.RawMessage(tc.body))
				}
				if err == nil && result.Err == nil {
					t.Errorf("incomplete replacement accepted: %s", result.Text)
				}
				data, readErr := os.ReadFile(path)
				if readErr != nil || string(data) != original {
					t.Fatalf("invalid edit changed file: %q, %v", data, readErr)
				}
			})
		}
	}
}

func TestPatchExplicitEmptyReplacementStillDeletes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("alpha beta\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := core.NewRegistry()
	spec := NewPatchFile(root).Spec()
	reg.MustRegister(spec)
	args, _ := json.Marshal(map[string]any{"path": "a.txt", "old": "alpha ", "new": ""})
	result, err := reg.Execute(context.Background(), spec.Name, args)
	if err != nil || result.Err != nil {
		t.Fatalf("explicit deletion rejected: err=%v, result=%+v", err, result)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "beta\n" {
		t.Fatalf("deletion result: %q, %v", data, err)
	}
}
