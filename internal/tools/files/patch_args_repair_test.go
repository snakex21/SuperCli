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

func TestRedundantPatchPathsKeepAtomicityAndExactScope(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
		ok              bool
	}{
		{name: "identical paths", raw: "{\"path\":\"a.txt\",\"changes\":[{\"path\":\"a.txt\",\"old\":\"first\",\"new\":\"ONE\"},{\"path\":\"a.txt\",\"old\":\"second\",\"new\":\"TWO\"}]}", want: "ONE TWO", ok: true},
		{name: "mixed presence", raw: "{\"path\":\"a.txt\",\"changes\":[{\"path\":\"a.txt\",\"old\":\"first\",\"new\":\"ONE\"},{\"old\":\"second\",\"new\":\"TWO\"}]}", want: "ONE TWO", ok: true},
		{name: "delete text", raw: "{\"path\":\"a.txt\",\"changes\":[{\"path\":\"a.txt\",\"old\":\"first \",\"new\":\"\"}]}", want: "second", ok: true},
		{name: "conflicting nested target", raw: "{\"path\":\"a.txt\",\"changes\":[{\"path\":\"a.txt\",\"old\":\"first\",\"new\":\"ONE\"},{\"path\":\"b.txt\",\"old\":\"second\",\"new\":\"TWO\"}]}"},
		{name: "missing root", raw: "{\"changes\":[{\"path\":\"a.txt\",\"old\":\"first\",\"new\":\"ONE\"}]}"},
		{name: "path spelling differs", raw: "{\"path\":\"a.txt\",\"changes\":[{\"path\":\"./a.txt\",\"old\":\"first\",\"new\":\"ONE\"}]}"},
		{name: "root duplicate before scalar coercion", raw: "{\"path\":\"b.txt\",\"path\":\"a.txt\",\"expected_count\":\"1\",\"changes\":[{\"path\":\"a.txt\",\"old\":\"first\",\"new\":\"ONE\"}]}"},
		{name: "nested duplicate", raw: "{\"path\":\"a.txt\",\"changes\":[{\"path\":\"b.txt\",\"path\":\"a.txt\",\"old\":\"first\",\"new\":\"ONE\"}]}"},
		{name: "other unknown key", raw: "{\"path\":\"a.txt\",\"changes\":[{\"path\":\"a.txt\",\"old\":\"first\",\"new\":\"ONE\",\"extra\":true}]}"},
		{name: "wrong nested type", raw: "{\"path\":\"a.txt\",\"changes\":[{\"path\":7,\"old\":\"first\",\"new\":\"ONE\"}]}"},
		{name: "count still enforced", raw: "{\"path\":\"a.txt\",\"changes\":[{\"path\":\"a.txt\",\"old\":\"first\",\"new\":\"ONE\",\"expected_count\":2}]}"},
		{name: "stale hash still enforced", raw: "{\"path\":\"a.txt\",\"base_hash\":\"stale\",\"changes\":[{\"path\":\"a.txt\",\"old\":\"first\",\"new\":\"ONE\"}]}"},
		{name: "later failed replacement stays atomic", raw: "{\"path\":\"a.txt\",\"changes\":[{\"path\":\"a.txt\",\"old\":\"first\",\"new\":\"ONE\"},{\"path\":\"a.txt\",\"old\":\"missing\",\"new\":\"TWO\"}]}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{"a.txt", "b.txt"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte("first second"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			reg := core.NewRegistry()
			reg.MustRegister(NewPatchFile(root).Spec())
			result, err := reg.Execute(context.Background(), "patch_file", json.RawMessage(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if (result.Err == nil) != tc.ok {
				t.Fatalf("success=%t error=%v", result.Err == nil, result.Err)
			}
			want := tc.want
			if !tc.ok {
				want = "first second"
			}
			got, _ := os.ReadFile(filepath.Join(root, "a.txt"))
			other, _ := os.ReadFile(filepath.Join(root, "b.txt"))
			if string(got) != want || string(other) != "first second" {
				t.Fatalf("unexpected writes: a=%q b=%q", got, other)
			}
		})
	}
}

func TestPatchPathRepairPreservesLiteralPayload(t *testing.T) {
	old := "literal \"path\": \"other\"\n\tzażółć\\new\r\n"
	newText := "literal \"new\": \"\"\nreplacement\t🙂"
	raw, _ := json.Marshal(map[string]any{"path": "ą.txt", "changes": []any{map[string]any{"path": "ą.txt", "old": old, "new": newText, "expected_count": 1}}})
	fixed, ok := repairRedundantPatchPaths(raw)
	if !ok {
		t.Fatal("not repaired")
	}
	var args patchFileArgs
	if err := json.Unmarshal(fixed, &args); err != nil {
		t.Fatal(err)
	}
	if args.Path != "ą.txt" || args.Changes[0].Old == nil || *args.Changes[0].Old != old || args.Changes[0].New == nil || *args.Changes[0].New != newText || args.Changes[0].ExpectedCount != 1 {
		t.Fatal("literal payload changed")
	}
	if _, ok := repairRedundantPatchPaths(fixed); ok {
		t.Fatal("repair is not idempotent")
	}
	if !strings.Contains(string(raw), "other") {
		t.Fatal("input mutated")
	}
}

func BenchmarkPatchPathArgumentRepair(b *testing.B) {
	for _, kind := range []string{"valid", "redundant", "conflicting"} {
		b.Run(kind, func(b *testing.B) {
			spec := NewPatchFile(".").Spec()
			spec.Fn = func(context.Context, json.RawMessage) (Result, error) { return Result{Text: "fixture validated"}, nil }
			reg := core.NewRegistry()
			reg.MustRegister(spec)
			nested := ""
			if kind == "redundant" {
				nested = "\"path\":\"a.txt\","
			}
			if kind == "conflicting" {
				nested = "\"path\":\"b.txt\","
			}
			raw := json.RawMessage("{\"path\":\"a.txt\",\"changes\":[{" + nested + "\"old\":\"before\",\"new\":\"after\"}]}")
			b.ReportAllocs()
			for b.Loop() {
				_, _ = reg.Execute(context.Background(), "patch_file", raw)
			}
		})
	}
}

func TestRedundantPatchPathCannotEscapeSandbox(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	outside := filepath.Join(parent, "outside.txt")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage("{\"path\":\"../outside.txt\",\"changes\":[{\"path\":\"../outside.txt\",\"old\":\"untouched\",\"new\":\"changed\"}]}")
	reg := core.NewRegistry()
	reg.MustRegister(NewPatchFile(root).Spec())
	result, err := reg.Execute(context.Background(), "patch_file", raw)
	if err != nil || result.Err == nil {
		t.Fatalf("sandbox rejection lost: %v %v", err, result.Err)
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "untouched" {
		t.Fatalf("outside path modified: %q %v", got, err)
	}
}
