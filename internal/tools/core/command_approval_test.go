package core

import (
	"errors"
	"path/filepath"
	"testing"

	"supercli/internal/tools/sandbox"
)

// The guard never asks: commands it cannot judge, git reset --hard and
// deletes outside the project run without an approval prompt; literal
// catastrophes are refused.
func TestCommandApprovalNeverAsks(t *testing.T) {
	project := t.TempDir()
	for _, argv := range [][]string{
		{"git", "reset", "--hard", "HEAD"},
		{"rm", "-rf", filepath.Join(filepath.Dir(project), "elsewhere")},
		{"powershell", "-NoProfile", "-Command", "$ErrorActionPreference='Stop'; $p=(Get-Location).Path; Start-Process zig.exe -Wait"},
	} {
		req, err := CommandApproval("ctx_execute", argv, sandbox.CheckCommand(argv, project, ""))
		if err != nil || req != nil {
			t.Errorf("%q: req=%+v err=%v, want it to run", argv, req, err)
		}
	}
	argv := []string{"rm", "-rf", "/"}
	if _, err := CommandApproval("ctx_execute", argv, sandbox.CheckCommand(argv, project, "")); !errors.Is(err, ErrDestructiveCommand) {
		t.Errorf("rm -rf / not blocked: %v", err)
	}
}
