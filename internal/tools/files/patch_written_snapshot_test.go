package files

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPatchFileWrittenSnapshot(t *testing.T) {
	cases := []struct{ name, before, args, want, absent string }{
		{"exact", "one\nconst Value = 1\nthree\n", `{"old":"Value = 1","new":"Value = 2"}`, "   2 | const Value = 2", "Value = 1"},
		{"relaxed", "func main() {\r\n\tvalue := 1\r\n\tuse(value)\r\n}\r\n", `{"old":"\tvalue := 1\n\tuse(value)","new":"\tvalue := 2\n\tuse(value)"}`, "   2 | \tvalue := 2", "Value = 1"},
		{"chained", "value = 1\n", `{"changes":[{"old":"1","new":"2"},{"old":"2","new":"3"}]}`, "   1 | value = 3", "value = 2"},
		{"empty", "delete all\n", `{"old":"delete all\n","new":""}`, "empty file", "delete all"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "sample.txt")
			if err := os.WriteFile(path, []byte(tc.before), 0600); err != nil {
				t.Fatal(err)
			}
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.args), &args); err != nil {
				t.Fatal(err)
			}
			args["path"] = "sample.txt"
			raw, _ := json.Marshal(args)
			out, err := NewPatchFile(root).execute(t.Context(), raw)
			if err != nil || out.Err != nil {
				t.Fatalf("patch failed: %v %+v", err, out)
			}
			_, preview, ok := strings.Cut(out.Text, "\n[Written snapshot")
			if !ok || !strings.Contains(preview, tc.want) || strings.Contains(preview, tc.absent) {
				t.Fatalf("wrong written snapshot: %s", out.Text)
			}
			if strings.Contains(preview, "\r") {
				t.Fatalf("CR leaked into display: %q", preview)
			}
			if err := os.WriteFile(path, []byte("external later edit"), 0600); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(preview, tc.want) {
				t.Fatal("snapshot mutated")
			}
		})
	}
}

func TestPatchFileWrittenSnapshotBounded(t *testing.T) {
	for _, body := range []string{
		strings.Repeat("unchanged line\n", 10000) + "target=old\n" + strings.Repeat("later line\n", 10000),
		"target=" + strings.Repeat("źółw", 1500) + "old\n",
	} {
		t.Run("bounded", func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			out, err := NewPatchFile(root).execute(t.Context(), json.RawMessage(`{"path":"sample.txt","old":"old","new":"new"}`))
			if err != nil || out.Err != nil {
				t.Fatalf("patch failed: %v %+v", err, out)
			}
			_, preview, ok := strings.Cut(out.Text, "\n[Written snapshot")
			if !ok || len("[Written snapshot"+preview) > 1024 || !utf8.ValidString(preview) || !strings.Contains(preview, "Partial snapshot") {
				t.Fatalf("unbounded or misleading snapshot: %s", out.Text)
			}
			if strings.Contains(body, "unchanged line") && !strings.Contains(preview, "10001 | target=new") {
				t.Fatalf("wrong line offset: %s", preview)
			}
		})
	}
}

func TestPatchFileWrittenSnapshotOnlyAfterWrite(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.txt")
	_ = os.WriteFile(path, []byte("same\n"), 0600)
	for _, args := range []string{
		`{"path":"sample.txt","old":"same","new":"same"}`,
		`{"path":"sample.txt","old":"missing","new":"new"}`,
		`{"path":"sample.txt","old":"same","new":"new","base_hash":"stale"}`,
	} {
		out, _ := NewPatchFile(root).execute(t.Context(), json.RawMessage(args))
		if strings.Contains(out.Text, "Written snapshot") {
			t.Fatalf("snapshot without successful write: %+v", out)
		}
	}
}
