package ctxexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestResolveWindowsDirLiteralMapping(t *testing.T) {
	root := t.TempDir()
	native := filepath.Join(root, "System32", "cmd.exe")
	lookup := func(name string) (string, error) {
		if strings.EqualFold(name, "dir") {
			return "", exec.ErrNotFound
		}
		return native, nil
	}
	for _, tc := range []struct {
		command []string
		script  string
	}{
		{[]string{"dir"}, "dir"},
		{[]string{"DIR", "/B", "/a", `C:\fixture with spaces\żółć\资料`}, `dir /B /a "C:\fixture with spaces\żółć\资料"`},
		{[]string{"dir", `*.txt`}, `dir "*.txt"`},
		{[]string{"dir", ".", "/b"}, `dir "." /b`},
		{[]string{"cmd", "/c", "dir"}, "dir"},
		{[]string{"cmd.exe", "/s", "/d", "/c", "dir", `C:\fixture with spaces`, "/b"}, `dir "C:\fixture with spaces" /b`},
	} {
		original := append([]string(nil), tc.command...)
		mapping, ok := ResolveWindowsDirCommand(tc.command, nil, root, lookup)
		if !ok {
			t.Fatalf("literal mapping rejected: %q", tc.command)
		}
		want := []string{native, "/d", "/s", "/c", tc.script}
		if !reflect.DeepEqual(mapping.effective, want) || !reflect.DeepEqual(tc.command, original) {
			t.Fatalf("mapping=%q want=%q original mutated=%v", mapping.effective, want, !reflect.DeepEqual(tc.command, original))
		}
	}
}

func TestResolveWindowsDirRejectsUnsafeAndCustomRequests(t *testing.T) {
	root := t.TempDir()
	native := filepath.Join(root, "System32", "cmd.exe")
	lookup := func(name string) (string, error) {
		if strings.EqualFold(name, "dir") {
			return "", exec.ErrNotFound
		}
		return native, nil
	}
	for _, command := range [][]string{
		{}, {"dir.exe"}, {"tools/dir"}, {"dir", ""}, {"dir", "one", "two"},
		{"dir", "/s"}, {"dir", "/p"}, {"dir", "/b /a"}, {"dir", `"quoted"`},
		{"dir", "&"}, {"dir", "a|b"}, {"dir", "a>b"}, {"dir", "a<b"},
		{"dir", "a^b"}, {"dir", "%PATH%"}, {"dir", "!PATH!"}, {"dir", "(a)"},
		{"dir", "`a`"}, {"dir", "$(a)"}, {"dir", "a\tb"}, {"dir", "a\r\nb"}, {"dir", "a\x00b"}, {"dir", "a\x7fb"}, {"dir", "a\u0085b"},
		{"cmd", "/c", "dir & echo changed>changed.txt"},
	} {
		if _, ok := ResolveWindowsDirCommand(command, nil, root, lookup); ok {
			t.Errorf("unsafe mapping accepted: %q", command)
		}
	}
	for _, env := range [][]string{{"PATH=fixture"}, {"COMSPEC=fixture"}, {"SystemRoot=fixture"}, {"DIRCMD=/p"}, {"FIXTURE=1"}} {
		if _, ok := ResolveWindowsDirCommand([]string{"dir"}, env, root, lookup); ok {
			t.Errorf("environment override accepted: %q", env)
		}
	}
	for _, lookupErr := range []error{nil, exec.ErrDot, os.ErrPermission, errors.New("unknown lookup failure")} {
		custom := func(name string) (string, error) {
			if name == "dir" {
				return filepath.Join(root, "fixture", "dir.exe"), lookupErr
			}
			return native, nil
		}
		if _, ok := ResolveWindowsDirCommand([]string{"dir"}, nil, root, custom); ok {
			t.Errorf("custom or uncertain dir mapping accepted: %v", lookupErr)
		}
	}
	customCmd := func(name string) (string, error) {
		if name == "dir" {
			return "", exec.ErrNotFound
		}
		return filepath.Join(root, "fixture", "cmd.exe"), nil
	}
	if _, ok := ResolveWindowsDirCommand([]string{"dir"}, nil, root, customCmd); ok {
		t.Fatal("custom cmd mapped")
	}
	if _, ok := ResolveWindowsDirCommand([]string{"dir"}, nil, "relative-root", lookup); ok {
		t.Fatal("relative native root mapped")
	}
}

func TestWindowsDirBindingMatchesOriginalRequest(t *testing.T) {
	root := t.TempDir()
	command := []string{"dir", "/b", "fixture with spaces"}
	mapping, ok := ResolveWindowsDirCommand(command, nil, root, func(name string) (string, error) {
		if name == "dir" {
			return "", exec.ErrNotFound
		}
		return filepath.Join(root, "System32", "cmd.exe"), nil
	})
	if !ok {
		t.Fatal("synthetic native mapping failed")
	}
	ctx := WithWindowsDirCommand(context.Background(), mapping, "project")
	command[1] = "/p"
	matching := &Request{Command: []string{"dir", "/b", "fixture with spaces"}, Workdir: "project"}
	if got, ok := boundWindowsDirCommand(ctx, matching); !ok || !reflect.DeepEqual(got, mapping.effective) {
		t.Fatal("binding lost admitted command")
	}
	for _, req := range []*Request{
		{Command: []string{"dir", "/p", "fixture with spaces"}, Workdir: "project"},
		{Command: matching.Command, EnvExtra: []string{"PATH=custom"}, Workdir: "project"},
		{Command: matching.Command, Workdir: "other"},
	} {
		if _, ok := boundWindowsDirCommand(ctx, req); ok {
			t.Fatalf("nested request inherited unrelated mapping: %+v", req)
		}
	}
}

func TestRunnerWindowsDirUsesBoundNativeExecutable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows cmd fixture")
	}
	home := t.TempDir()
	path := filepath.Join(home, "fixture with spaces")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "source.txt"), []byte("fixture\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range [][]string{{"dir", "/b", path}, {"cmd", "/c", "dir", "/b", path}} {
		mapping, ok := ResolveWindowsDirCommand(command, nil, os.Getenv("SystemRoot"), exec.LookPath)
		if !ok {
			t.Skip("ambient resolver cannot admit native fixture")
		}
		runner := New(home)
		lookups := 0
		runner.LookPath = func(string) (string, error) { lookups++; return "", errors.New("PATH changed after admission") }
		ctx, cancel := context.WithTimeout(WithWindowsDirCommand(context.Background(), mapping, ""), 10*time.Second)
		result, err := runner.Run(ctx, &Request{Command: command})
		cancel()
		if err != nil || result.ExitCode != 0 || !strings.Contains(result.Stdout, "source.txt") {
			t.Fatalf("bound listing failed: result=%+v err=%v", result, err)
		}
		if lookups != 0 || result.Command != strings.Join(command, " ") {
			t.Fatalf("bound execution performed lookup or changed receipt: lookups=%d command=%q", lookups, result.Command)
		}
	}
}

func TestRunnerWindowsBareDirAndRejectedShellFallback(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows cmd fixture")
	}
	if _, ok := ResolveWindowsDirCommand([]string{"dir"}, nil, os.Getenv("SystemRoot"), exec.LookPath); !ok {
		t.Skip("ambient resolver cannot admit native fixture")
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "source.txt"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := New(home)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := runner.Run(ctx, &Request{Command: []string{"dir", "/b"}})
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Stdout, "source.txt") {
		t.Fatalf("bare listing failed: %+v err=%v", result, err)
	}
	result, err = runner.Run(ctx, &Request{Command: []string{"dir", "/b", "&", "echo", "changed>changed.txt"}})
	if err != nil || result.ExitCode != ExitNotFound {
		t.Fatalf("unsafe bare request acquired a shell: %+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(home, "changed.txt")); !os.IsNotExist(err) {
		t.Fatalf("unsafe bare request mutated workspace: %v", err)
	}
}

func TestWindowsDirCustomExecutableProcess(t *testing.T) {
	if os.Getenv("SUPERCLI_DIR_FIXTURE") != "1" {
		return
	}
	os.Stdout.WriteString("custom-dir-argv-preserved\n")
	os.Exit(0)
}

func TestRunnerPreservesCustomDirExecutable(t *testing.T) {
	runner := New(t.TempDir())
	runner.LookPath = func(name string) (string, error) {
		if name != "dir" {
			return "", errors.New("custom dir must not look up a shell")
		}
		return os.Args[0], nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, env := range [][]string{nil, {"SUPERCLI_DIR_FIXTURE=1"}} {
		result, err := runner.Run(ctx, &Request{Command: []string{"dir", "-test.run=^TestWindowsDirCustomExecutableProcess$"}, EnvExtra: env})
		want := "PASS"
		if len(env) != 0 {
			want = "custom-dir-argv-preserved"
		}
		if err != nil || result.ExitCode != 0 || !strings.Contains(result.Stdout, want) {
			t.Fatalf("custom executable changed semantics: %+v err=%v", result, err)
		}
	}
}
