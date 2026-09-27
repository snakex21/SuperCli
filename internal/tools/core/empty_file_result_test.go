package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpectedEmptyFileKeepsOtherVerificationChecks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "empty.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path, want string
		expected, ok     bool
	}{
		{"intentional", "empty.txt", "", true, true},
		{"unexpected", "empty.txt", "", false, false},
		{"missing", "missing.txt", "", true, false},
		{"content still required", "empty.txt", "must be present", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, _ := json.Marshal(map[string]any{"path": tc.path, "must_contain": tc.want})
			verdict := (DefaultVerifier{}).Verify(Check{
				Tool: "patch_file", BaseDir: root, Args: args,
				Result: Result{Text: "Patched successfully", EmptyFileExpected: tc.expected},
			})
			if verdict.OK != tc.ok {
				t.Fatalf("OK=%v want=%v reason=%s", verdict.OK, tc.ok, verdict.Reason)
			}
		})
	}
}

func TestEmptyFileEvidenceNeverEntersModelOrJSON(t *testing.T) {
	result := Result{Text: "Created .gitkeep (0 bytes)", EmptyFileExpected: true}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "EmptyFileExpected") {
		t.Fatalf("internal evidence leaked into JSON: %s", data)
	}
	if got := result.ModelContent(); got != result.Text {
		t.Fatalf("internal evidence changed model output: %s", got)
	}
}
