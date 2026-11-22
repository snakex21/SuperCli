package core

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools/sandbox"
)

func TestCommandApprovalWording(t *testing.T) {
	project := t.TempDir()
	argv := []string{"git", "reset", "--hard", "HEAD"}
	req, err := CommandApproval("ctx_execute", argv, sandbox.CheckCommand(argv, project, ""))
	if err != nil || req == nil {
		t.Fatalf("git reset --hard: req=%v err=%v, want an approval request", req, err)
	}
	if !strings.Contains(req.Summary, "discards uncommitted changes in the project") || !strings.Contains(req.Summary, "Run it?") {
		t.Errorf("summary = %q", req.Summary)
	}
	if strings.Contains(strings.ToLower(req.Summary), "never run it yourself") {
		t.Errorf("an approval prompt tells the model never to run it: %q", req.Summary)
	}

	argv = []string{"rm", "-rf", filepath.Join(filepath.Dir(project), "elsewhere")}
	req, err = CommandApproval("ctx_execute", argv, sandbox.CheckCommand(argv, project, ""))
	if err != nil || req == nil || !strings.HasPrefix(req.Summary, "This command may delete, move or overwrite files") ||
		strings.Contains(strings.ToLower(req.Summary), "never run it yourself") {
		t.Errorf("delete outside the project: req=%+v err=%v", req, err)
	}

	argv = []string{"rm", "-rf", "/"}
	if _, err := CommandApproval("ctx_execute", argv, sandbox.CheckCommand(argv, project, "")); !errors.Is(err, ErrDestructiveCommand) {
		t.Errorf("rm -rf / not blocked: %v", err)
	}
}
