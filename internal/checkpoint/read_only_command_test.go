package checkpoint

import (
	"encoding/json"
	"testing"
)

func TestCheckpointCommandReadOnly(t *testing.T) {
	for _, command := range [][]string{
		{"git", "status"}, {"git", "status", "--short"}, {"git.exe", "status", "-sb"},
		{"git", "status", "--porcelain=v2", "--untracked-files=no"},
		{"git", "rev-parse", "HEAD"}, {"git", "rev-parse", "--show-toplevel"},
		{"git", "branch", "-a"}, {"git", "log", "-n", "5", "--oneline"},
		{"git", "log", "--all", "-8", "--oneline", "--decorate"},
	} {
		raw, _ := json.Marshal(map[string]any{"command": command})
		if !checkpointCommandReadOnly("ctx_execute", raw) {
			t.Errorf("read-only inspection requires no workspace snapshot: %v", command)
		}
	}
	for _, command := range [][]string{
		{"git", "branch", "new-branch"}, {"git", "branch", "-D", "main"},
		{"git", "-c", "alias.inspect=!touch x", "inspect"},
		{"git", "log", "--output=created.txt"}, {"git", "log", "--textconv", "-p"},
		{"git", "diff"}, {"git", "show", "HEAD"},
		{"tools/git.exe", "status"},
		{"git", "log", "-n"}, {"git", "log", "-n", "0"},
		{"git", "status", "--", "other"}, {"git", "status", "--unknown"},
		{"cmd", "/c", "git status & echo x > file"},
		{"python", "test_script.py"}, {"rg", "--pre", "writer", "pattern"},
	} {
		raw, _ := json.Marshal(map[string]any{"command": command})
		if checkpointCommandReadOnly("ctx_execute", raw) {
			t.Errorf("unknown or mutating command bypassed checkpoint: %v", command)
		}
	}
	if checkpointCommandReadOnly("patch_file", json.RawMessage(`{"command":["git","status"]}`)) ||
		checkpointCommandReadOnly("ctx_execute", json.RawMessage(`{"command":"git status"}`)) {
		t.Fatal("only structured ctx_execute commands may bypass snapshots")
	}
}
