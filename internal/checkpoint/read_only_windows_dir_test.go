package checkpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"supercli/internal/tools/ctxexec"
	"supercli/internal/tools/workflow"
)

func TestCheckpointWindowsDirStrictAdmission(t *testing.T) {
	systemRoot := t.TempDir()
	nativeCmd := filepath.Join(systemRoot, "System32", "cmd.exe")
	lookPath := func(string) (string, error) { return nativeCmd, nil }
	for _, command := range [][]string{
		{"cmd", "/c", "dir"},
		{"cmd.exe", "/d", "/c", "dir /b /a ."},
		{"CMD.EXE", "/D", "/S", "/C", `DIR /B "C:\fixture with spaces"`},
		{"cmd", "/c", "dir", `C:\fixture with spaces`, "/b"},
		{"cmd", "/c", "dir", `"C:\fixture with spaces"`, "/a"},
		{"cmd", "/c", " dir\t/b\t资料 "},
		{"cmd", "/c", `dir "C:\żółć\资料"`},
	} {
		if !checkpointWindowsDirReadOnly(command, nil, systemRoot, lookPath) {
			t.Errorf("literal builtin listing rejected: %q", command)
		}
	}
	for _, command := range [][]string{
		{"dir", `C:\fixture`},
		{"dir.exe", `C:\fixture`},
		{nativeCmd, "/c", "dir"},
		{"tools/cmd.exe", "/c", "dir"},
		{"cmd", "/k", "dir"},
		{"cmd", "/d", "/d", "/c", "dir"},
		{"cmd", "/c"},
		{"cmd", "/c", ""},
		{"cmd", "/c", `"dir" "C:\fixture"`},
		{"cmd", "/c", `directory C:\fixture`},
		{"cmd", "/c", "dir C:\\fixture other"},
		{"cmd", "/c", "dir /s"},
		{"cmd", "/c", "dir /p"},
		{"cmd", "/c", "dir /unknown"},
		{"cmd", "/c", "dir & echo changed>changed.txt"},
		{"cmd", "/c", "dir", "&&", "echo", "changed"},
		{"cmd", "/c", "dir | another-command"},
		{"cmd", "/c", "dir >listing.txt"},
		{"cmd", "/c", "dir <input.txt"},
		{"cmd", "/c", `dir "C:\fixture & echo changed"`},
		{"cmd", "/c", `dir %FIXTURE%`},
		{"cmd", "/c", `dir !FIXTURE!`},
		{"cmd", "/c", `dir ^& echo changed`},
		{"cmd", "/c", `dir (fixture)`},
		{"cmd", "/c", "dir `fixture`"},
		{"cmd", "/c", "dir $(fixture)"},
		{"cmd", "/c", "dir\r\necho changed"},
		{"cmd", "/c", `dir "unterminated`},
		{"cmd", "/c", `dir "fixture"suffix`},
		{"cmd", "/c", `dir fixture"suffix`},
		{"cmd", "/c", `dir ""`},
	} {
		if checkpointWindowsDirReadOnly(command, nil, systemRoot, lookPath) {
			t.Errorf("unknown or mutating command bypassed capture: %q", command)
		}
	}
}

func TestCheckpointWindowsDirRejectsCustomCmdAndEnvironment(t *testing.T) {
	systemRoot := t.TempDir()
	command := []string{"cmd", "/d", "/c", "dir /b"}
	nativeCmd := filepath.Join(systemRoot, "System32", "cmd.exe")
	lookPath := func(string) (string, error) { return nativeCmd, nil }
	for _, env := range [][]string{
		{"PATH=C:\\fixture-bin"},
		{"COMSPEC=C:\\fixture-bin\\cmd.exe"},
		{"SystemRoot=C:\\fixture-root"},
		{"DIRCMD=/p"},
		{"FIXTURE=1"},
	} {
		if checkpointWindowsDirReadOnly(command, env, systemRoot, lookPath) {
			t.Errorf("environment override bypassed capture: %q", env)
		}
	}
	custom := func(string) (string, error) { return filepath.Join(systemRoot, "fixture-bin", "cmd.exe"), nil }
	if checkpointWindowsDirReadOnly(command, nil, systemRoot, custom) {
		t.Fatal("custom cmd.exe from PATH bypassed capture")
	}
	missing := func(string) (string, error) { return "", errors.New("fixture missing executable") }
	if checkpointWindowsDirReadOnly(command, nil, systemRoot, missing) ||
		checkpointWindowsDirReadOnly(command, nil, "", lookPath) ||
		checkpointWindowsDirReadOnly(command, nil, "relative-root", lookPath) {
		t.Fatal("unknown native cmd identity bypassed capture")
	}
	called := false
	countLookup := func(string) (string, error) { called = true; return nativeCmd, nil }
	if checkpointWindowsDirReadOnly([]string{"dir", "."}, nil, systemRoot, countLookup) || !called {
		t.Fatal("resolved executable named dir must retain capture")
	}
	missingDir := func(name string) (string, error) {
		if strings.EqualFold(name, "dir") {
			return "", exec.ErrNotFound
		}
		return nativeCmd, nil
	}
	for _, command := range [][]string{{"dir"}, {"dir", "/b", `C:\fixture with spaces`}, {"DIR", "/a", "."}} {
		if !checkpointWindowsDirReadOnly(command, nil, systemRoot, missingDir) {
			t.Errorf("native bare listing rejected: %q", command)
		}
		if checkpointWindowsDirReadOnly(command, []string{"PATH=custom"}, systemRoot, missingDir) {
			t.Errorf("bare listing environment override bypassed capture: %q", command)
		}
	}
}

func TestCheckpointWindowsDirRealWrappedTurn(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows cmd fixture")
	}
	if !checkpointWindowsDirReadOnly([]string{"cmd", "/d", "/c", "dir /b"}, nil, os.Getenv("SystemRoot"), exec.LookPath) {
		t.Skip("ambient PATH does not resolve native System32 cmd.exe")
	}
	for _, controllerWrap := range []bool{false, true} {
		for _, mode := range []string{"literal-listing", "bare-listing", "bare-path", "operator-mutation", "bare-operator-rejected"} {
			mutating := mode == "operator-mutation"
			rejected := mode == "bare-operator-rejected"
			name := "turn"
			if controllerWrap {
				name = "controller"
			}
			name += "/" + mode
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				home, data := filepath.Join(root, "workspace"), filepath.Join(root, "data")
				if err := os.MkdirAll(home, 0700); err != nil {
					t.Fatal(err)
				}
				before := []byte("exact before\r\n")
				if err := os.WriteFile(filepath.Join(home, "source.txt"), before, 0600); err != nil {
					t.Fatal(err)
				}
				manager, err := Open(home, data)
				if errors.Is(err, ErrUnavailable) {
					t.Skip(err)
				}
				if err != nil {
					t.Fatal(err)
				}
				turn := manager.NewTurn("dir-fixture", "synthetic directory inspection")
				runner := ctxexec.New(home)
				if !mutating && !rejected {
					// The wrapper's native decision must survive a later resolver
					// change. Execution must not repeat admission's PATH lookup.
					runner.LookPath = func(string) (string, error) {
						t.Error("admitted native listing repeated PATH lookup")
						return "", exec.ErrNotFound
					}
				}
				spec := workflow.NewCtxExecuteTool(runner, home).Spec()
				if controllerWrap {
					controller := NewController(manager, "dir-fixture")
					controller.Start("synthetic directory inspection")
					turn = controller.currentTurn()
					spec = controller.Wrap(spec)
				} else {
					spec = turn.Wrap(spec)
				}
				t.Cleanup(func() {
					cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					_, _ = turn.Complete(cleanup)
				})
				command := "dir /b"
				if mutating {
					command += " & echo changed>changed.txt"
				}
				argv := []string{"cmd", "/d", "/c", command}
				switch mode {
				case "bare-listing":
					argv = []string{"dir", "/b"}
				case "bare-path":
					argv = []string{"dir", "/b", home}
				case "bare-operator-rejected":
					argv = []string{"dir", "/b", "&", "echo", "changed>changed.txt"}
				}
				if strings.HasPrefix(mode, "bare-") && !checkpointWindowsDirReadOnly([]string{"dir"}, nil, os.Getenv("SystemRoot"), exec.LookPath) {
					t.Skip("ambient dir executable prevents builtin fallback")
				}
				raw, err := json.Marshal(map[string]any{"command": argv})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				result, err := spec.Fn(ctx, raw)
				if err != nil || (!rejected && result.Err != nil) {
					t.Fatalf("wrapped execution: err=%v result=%v", err, result.Err)
				}
				var output ctxexec.Result
				if err := json.Unmarshal([]byte(result.Text), &output); err != nil {
					t.Fatal(err)
				}
				if rejected {
					if output.ExitCode != ctxexec.ExitNotFound || result.Err == nil {
						t.Fatalf("unsafe bare listing used shell fallback: %+v", output)
					}
				} else if output.ExitCode != 0 || !strings.Contains(output.Stdout, "source.txt") {
					t.Fatalf("listing failed: %+v", output)
				}
				if !mutating && !rejected {
					if turn.touched || turn.before != "" || turn.active != nil {
						t.Fatal("literal dir acquired a before snapshot or active pin")
					}
					if _, err := os.Stat(manager.repo); !os.IsNotExist(err) {
						t.Fatalf("literal dir initialized a checkpoint repository: %v", err)
					}
				} else if !turn.touched || turn.before == "" || turn.active == nil {
					t.Fatal("dir with mutation did not capture before execution")
				}
				record, err := turn.Complete(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if !mutating {
					if record != nil {
						t.Fatalf("listing recorded a checkpoint: %+v", record)
					}
					if _, err := os.Stat(filepath.Join(home, "changed.txt")); !os.IsNotExist(err) {
						t.Fatalf("rejected listing mutated workspace: %v", err)
					}
					return
				}
				if record == nil || len(record.Changes) != 1 || record.Changes[0] != (FileChange{Path: "changed.txt", Kind: "created"}) {
					t.Fatalf("mutation checkpoint: %+v", record)
				}
				if _, err := manager.Undo(ctx, record.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Join(home, "changed.txt")); !os.IsNotExist(err) {
					t.Fatalf("undo retained mutation: %v", err)
				}
				actual, err := os.ReadFile(filepath.Join(home, "source.txt"))
				if err != nil || !bytes.Equal(actual, before) {
					t.Fatalf("original bytes changed: %v", err)
				}
			})
		}
	}
}
