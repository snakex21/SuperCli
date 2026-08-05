package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "supercli/internal/tools/core"
)

// patch_file is the only edit path now, so the cheapest edit there is — one
// replacement, one line — must not cost the model a nested array. These tests
// pin the shorthand: the schema accepts it, the registry's validator accepts
// it, and it produces exactly the same result as the long form.

func runPatch(t *testing.T, dir, args string) core.Result {
	t.Helper()
	res, err := NewPatchFile(dir).Spec().Fn(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	return res
}

func TestPatchFileTool_ShorthandOneChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("package a\nconst X = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := runPatch(t, dir, `{"path":"a.go","old":"const X = 1","new":"const X = 2"}`)
	if res.Err != nil {
		t.Fatalf("shorthand rejected: %v", res.Err)
	}
	if !strings.Contains(res.Text, "replacements=1") {
		t.Errorf("text = %q", res.Text)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "package a\nconst X = 2\n" {
		t.Errorf("content = %q", got)
	}
}

// The shorthand deletes as well as replaces: an empty `new` is what
// delete_lines used to be for, and it must not be mistaken for a missing
// argument.
func TestPatchFileTool_ShorthandEmptyNewDeletes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := runPatch(t, dir, `{"path":"a.txt","old":"two\n","new":""}`); res.Err != nil {
		t.Fatalf("delete via empty new: %v", res.Err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "one\nthree\n" {
		t.Errorf("content = %q", got)
	}
}

func TestPatchFileTool_ShorthandExpectedCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("x\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := runPatch(t, dir, `{"path":"a.txt","old":"x","new":"y","expected_count":2}`); res.Err != nil {
		t.Fatalf("expected_count with shorthand: %v", res.Err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "y\ny\n" {
		t.Errorf("content = %q", got)
	}
}

// The real gate is the registry's schema validation, which runs before Fn. A
// shorthand the schema rejects would never reach the code above.
func TestPatchFileTool_ShorthandPassesSchemaValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := core.NewRegistry()
	spec := NewPatchFile(dir).Spec()
	reg.MustRegister(spec)
	reg.MarkAlwaysOn(spec.Name)
	res, err := reg.Execute(context.Background(), "patch_file",
		json.RawMessage(`{"path":"a.txt","old":"alpha","new":"ALPHA"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("schema rejected the shorthand: %v", res.Err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "ALPHA\n" {
		t.Errorf("content = %q", got)
	}
}

// --- the errors have to carry the fix ---

func TestPatchFileTool_NothingToChange(t *testing.T) {
	res := runPatch(t, t.TempDir(), `{"path":"a.txt"}`)
	if res.Err == nil {
		t.Fatal("expected an error")
	}
	msg := res.Err.Error()
	if !strings.Contains(msg, "old and new") || !strings.Contains(msg, "changes") {
		t.Errorf("error must name both forms, got: %s", msg)
	}
}

func TestPatchFileTool_NewWithoutOld(t *testing.T) {
	res := runPatch(t, t.TempDir(), `{"path":"a.txt","new":"whatever"}`)
	if res.Err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(res.Err.Error(), "add old") {
		t.Errorf("error must say what is missing, got: %s", res.Err)
	}
}

func TestPatchFileTool_BothFormsRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := runPatch(t, dir, `{"path":"a.txt","old":"alpha","new":"A","changes":[{"old":"alpha","new":"B"}]}`)
	if res.Err == nil {
		t.Fatal("sending both forms must be rejected, not silently half-applied")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "alpha\n" {
		t.Errorf("file must be untouched, got %q", got)
	}
}

// No artificial cap on how much one call may change: a large edit has to go
// through in ONE call, because every extra call is a whole round-trip to the
// model.
func TestPatchFileTool_ManyChangesInOneCall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	var body strings.Builder
	changes := make([]map[string]any, 0, 200)
	for i := 0; i < 200; i++ {
		// The brackets keep each anchor unique: without them "line-1" is a
		// substring of "line-100" and the call would fail on ambiguity rather
		// than on the cap this test is about.
		body.WriteString("[line-" + itoa(i) + "]\n")
		changes = append(changes, map[string]any{
			"old": "[line-" + itoa(i) + "]",
			"new": "[LINE-" + itoa(i) + "]",
		})
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"path": "big.txt", "changes": changes})
	res, err := NewPatchFile(dir).Spec().Fn(context.Background(), args)
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("200 changes in one call rejected: %v", res.Err)
	}
	if !strings.Contains(res.Text, "replacements=200") {
		t.Errorf("text = %q", res.Text)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "[line-") {
		t.Error("not every change was applied")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
