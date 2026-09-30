package ctxexec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWorkdirExecutableHelper(t *testing.T) {
	if os.Getenv("SUPERCLI_WORKDIR_FIXTURE") != "1" {
		return
	}
	wd, err := os.Getwd()
	if err != nil {
		os.Exit(97)
	}
	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(struct {
		Workdir string
		Args    []string
	}{wd, args})
	if os.Getenv("SUPERCLI_WORKDIR_FAIL") == "1" {
		os.Exit(7)
	}
	os.Exit(0)
}

// FerrumScope supplied tools/zig/.../zig.exe relative to workdir while the
// SuperCli process lived elsewhere. Run a real child without shell workarounds.
func TestRunnerExplicitExecutableUsesWorkdir(t *testing.T) {
	home := t.TempDir()
	workdir := filepath.Join(home, "project with spaces")
	helper := filepath.Join(workdir, "tools", "helper")
	if runtime.GOOS == "windows" {
		helper += ".exe"
	}
	if err := os.MkdirAll(filepath.Dir(helper), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workdir, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, data, 0700); err != nil {
		t.Fatal(err)
	}
	rel, _ := filepath.Rel(workdir, helper)
	for _, tc := range []struct {
		name, file, cwd string
		fail            bool
	}{
		{"relative", rel, workdir, false},
		{"dot relative", "." + string(filepath.Separator) + rel, workdir, false},
		{"forward slashes", filepath.ToSlash(rel), workdir, false},
		{"parent relative", filepath.Join("..", rel), filepath.Join(workdir, "nested"), false},
		{"absolute", helper, workdir, false},
		{"nonzero preserved", rel, workdir, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := []string{"SUPERCLI_WORKDIR_FIXTURE=1"}
			if tc.fail {
				env = append(env, "SUPERCLI_WORKDIR_FAIL=1")
			}
			literal := "literal space $value & quote\""
			res, err := New(home).Run(context.Background(), &Request{
				Command: []string{tc.file, "-test.run=^TestWorkdirExecutableHelper$", "--", literal},
				Workdir: tc.cwd, EnvExtra: env,
			})
			wantExit := 0
			if tc.fail {
				wantExit = 7
			}
			if err != nil || res.ExitCode != wantExit {
				t.Fatalf("err=%v result=%+v", err, res)
			}
			var got struct {
				Workdir string
				Args    []string
			}
			if err := json.Unmarshal([]byte(res.Stdout), &got); err != nil {
				t.Fatalf("%v: %s", err, res.Stdout)
			}
			// The child reports its physical cwd; Windows can expand an 8.3
			// spelling here. Require the exact directory, not a string alias.
			gotDir, gotErr := os.Stat(got.Workdir)
			wantDir, wantErr := os.Stat(tc.cwd)
			if gotErr != nil || wantErr != nil || !os.SameFile(gotDir, wantDir) || len(got.Args) != 1 || got.Args[0] != literal {
				t.Fatalf("cwd or arguments changed: %+v (stat errors: %v / %v)", got, gotErr, wantErr)
			}
		})
	}
}
