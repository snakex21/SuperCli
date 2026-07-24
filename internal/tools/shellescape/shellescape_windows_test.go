//go:build windows

package shellescape

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunner_QuotesReachCmdVerbatim is the regression test for the silent file
// corruption seen in production (session 04d18759210d946c, 2026-07-23): the
// model ran
//
//	echo ^<link rel="stylesheet" href="style.css"^> > index.html
//
// got exit code 0, and the file contained  <link rel=\"stylesheet\" ...>  .
// Go escapes embedded double quotes as \" when it joins argv into a Windows
// command line, but cmd.exe does not undo that escaping, so the backslashes
// land in the file. The command must reach cmd.exe byte for byte.
func TestRunner_QuotesReachCmdVerbatim(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner(dir)

	res := r.Run(context.Background(), `echo ^<link rel="stylesheet" href="style.css"^> > index.html`)
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0 (err=%q stderr=%q)", res.ExitCode, res.Error, res.Stderr)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	got := strings.TrimRight(string(raw), " \t\r\n")

	if strings.Contains(got, `\"`) {
		t.Errorf("file contains backslash-escaped quotes (silent corruption): %q", got)
	}
	if want := `<link rel="stylesheet" href="style.css">`; got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

// TestRunner_QuotesReachCmdOnStdout is the same defect observed on stdout
// instead of through a redirection, so the test does not depend on the file
// system.
func TestRunner_QuotesReachCmdOnStdout(t *testing.T) {
	r := NewRunner(t.TempDir())

	res := r.Run(context.Background(), `echo ^<link rel="stylesheet" href="style.css"^>`)
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0 (err=%q)", res.ExitCode, res.Error)
	}
	got := strings.TrimRight(res.Stdout, " \t\r\n")

	if strings.Contains(got, `\"`) {
		t.Errorf("stdout contains backslash-escaped quotes: %q", got)
	}
	if want := `<link rel="stylesheet" href="style.css">`; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// TestRunner_NestedQuotesAndApostrophes covers quoting shapes that are common
// in generated JSON and prose: apostrophes, single quotes (not special to
// cmd.exe) and double quotes nested inside a quoted run.
func TestRunner_NestedQuotesAndApostrophes(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{
			name:    "json object",
			command: `echo {"name":"it's","tags":["a","b"]}`,
			want:    `{"name":"it's","tags":["a","b"]}`,
		},
		{
			name:    "single quotes stay literal",
			command: `echo 'single' and "double" mixed`,
			want:    `'single' and "double" mixed`,
		},
		{
			name:    "apostrophe inside double quotes",
			command: `echo "it's a test"`,
			want:    `"it's a test"`,
		},
		{
			// "^" escapes the redirection characters for cmd.exe; the quotes
			// and backslashes must survive untouched.
			name:    "quoted path with spaces",
			command: `echo ^<script src="C:\Program Files\a b\x.js"^>^</script^>`,
			want:    `<script src="C:\Program Files\a b\x.js"></script>`,
		},
	}

	r := NewRunner(t.TempDir())
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := r.Run(context.Background(), tc.command)
			if res.ExitCode != 0 {
				t.Fatalf("exit = %d, want 0 (err=%q stderr=%q)", res.ExitCode, res.Error, res.Stderr)
			}
			got := strings.TrimRight(res.Stdout, " \t\r\n")
			if got != tc.want {
				t.Errorf("stdout = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRunner_PlainCommands guards against regressions for commands that carry
// no quotes at all: they must keep working exactly as before.
func TestRunner_PlainCommands(t *testing.T) {
	r := NewRunner(t.TempDir())

	res := r.Run(context.Background(), "echo hello world")
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0 (err=%q)", res.ExitCode, res.Error)
	}
	if got := strings.TrimSpace(res.Stdout); got != "hello world" {
		t.Errorf("stdout = %q, want %q", got, "hello world")
	}

	// Chained commands and cmd.exe builtins still go through the shell.
	res = r.Run(context.Background(), "echo one& echo two")
	if got := strings.TrimSpace(res.Stdout); !strings.Contains(got, "one") || !strings.Contains(got, "two") {
		t.Errorf("stdout = %q, want both 'one' and 'two'", got)
	}
}

// TestRunner_ExitCodeWindows checks that a failing command is still reported as
// a failure rather than a silent success.
func TestRunner_ExitCodeWindows(t *testing.T) {
	r := NewRunner(t.TempDir())

	res := r.Run(context.Background(), "exit 42")
	if res.ExitCode != 42 {
		t.Errorf("exit = %d, want 42 (err=%q)", res.ExitCode, res.Error)
	}

	res = r.Run(context.Background(), "type no_such_file_here.xyz")
	if res.ExitCode == 0 {
		t.Errorf("exit = 0 for missing file, want non-zero")
	}
	if res.Stderr == "" {
		t.Errorf("stderr empty for missing file, want a diagnostic")
	}
}

// TestRunner_CwdIsHomeWindows checks that the working directory is honoured on
// the raw command-line path too.
func TestRunner_CwdIsHomeWindows(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner(dir)
	res := r.Run(context.Background(), "cd")
	if got := strings.TrimSpace(res.Stdout); !strings.EqualFold(got, dir) {
		t.Errorf("cwd = %q, want %q", got, dir)
	}
}

// TestRunner_TimeoutWindows checks that the context timeout still kills the
// child and surfaces a non-zero exit code. The command is a cmd.exe builtin
// loop on purpose: CommandContext kills the shell itself, and a long-running
// grandchild (ping, timeout.exe) would keep the inherited output handles open
// past the deadline. That grandchild behaviour predates this test.
func TestRunner_TimeoutWindows(t *testing.T) {
	r := NewRunner(t.TempDir())
	r.Timeout = 200 * time.Millisecond
	start := time.Now()
	res := r.Run(context.Background(), `for /l %i in (1,1,2000000000) do @rem`)
	if res.ExitCode == 0 {
		t.Fatalf("exit = 0, want non-zero after timeout")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("timeout not enforced: took %v", elapsed)
	}
}
