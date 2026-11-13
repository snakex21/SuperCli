//go:build windows

package ctxexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cmd.exe executes only the first line of a /c script and silently drops the
// rest. Live benchmark (gpt-5.4-nano, "count active rows and write RESULT.txt")
// sent `cmd /c python -c "import csv\nc=0\n..."` 20 times: every run exited 0
// with empty output, nothing was written, and the model ended with a wrong
// count. The runner must refuse such a script with an actionable error.
func TestCmdMultilineScriptIsRefusedNotTruncated(t *testing.T) {
	home := t.TempDir()
	for _, args := range [][]string{
		{"cmd", "/c", "echo first\necho second > second.txt"},
		{"cmd.exe", "/C", "python -c \"import sys\r\nopen('second.txt','w').write('x')\""},
		{"cmd", "/d", "/s", "/c", "echo a", "\necho b > second.txt"},
	} {
		res, err := New(home).Run(context.Background(), &Request{Command: args})
		if err != nil {
			t.Fatalf("%q: unexpected Go error %v", args, err)
		}
		if res.ExitCode != ExitValidationError {
			t.Fatalf("%q: exit=%d stdout=%q, want validation error (cmd drops lines after the first)", args, res.ExitCode, res.Stdout)
		}
		if !strings.Contains(res.Error, "first line") || !strings.Contains(res.Error, "create_file") {
			t.Fatalf("%q: error is not actionable: %q", args, res.Error)
		}
		if _, statErr := os.Stat(filepath.Join(home, "second.txt")); statErr == nil {
			t.Fatalf("%q: the command must not run at all", args)
		}
		if s := res.FailureSummary(); !strings.Contains(s, "first line") {
			t.Fatalf("failure summary hides the reason: %q", s)
		}
	}
}

func TestCmdSingleLineScriptsStillRun(t *testing.T) {
	home := t.TempDir()
	for _, args := range [][]string{
		{"cmd", "/c", "echo one\n"},   // a lone trailing newline is harmless
		{"cmd", "/c", "echo one\r\n"}, // same with CRLF
		{"cmd", "/c", "echo one && echo two"},
	} {
		res, err := New(home).Run(context.Background(), &Request{Command: args})
		if err != nil || res.ExitCode != 0 || !strings.Contains(res.Stdout, "one") {
			t.Fatalf("%q: err=%v result=%+v", args, err, res)
		}
	}
	// Non-cmd binaries keep argv semantics: a multi-line argument is fine.
	res, err := New(home).Run(context.Background(), &Request{Command: []string{"powershell", "-NoProfile", "-Command", "Write-Output one\nWrite-Output two"}})
	if err != nil || res.ExitCode != 0 || !strings.Contains(res.Stdout, "two") {
		t.Fatalf("powershell multi-line: err=%v result=%+v", err, res)
	}
}
