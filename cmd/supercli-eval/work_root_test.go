package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"supercli/internal/storage"
)

func TestEvalDefaultWorkRootUsesRuntimeDataRoot(t *testing.T) {
	base := t.TempDir()
	dataRoot := filepath.Join(base, "instance", "data")
	workspace := filepath.Join(base, "project")
	t.Setenv(storage.DataRootEnv, dataRoot)
	t.Setenv(storage.HomeEnv, workspace)
	got, err := prepareEvalWorkRoot("", storage.ResolveRuntimeDataRoot)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dataRoot, "eval", "workspaces")
	if got != want {
		t.Fatalf("work root = %q, want %q", got, want)
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("portable workspace root unavailable: %v", err)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("workspace home was used for runtime data: %v", err)
	}
}

func TestEvalExplicitWorkRootIsUnchanged(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)
	for _, path := range []string{filepath.FromSlash("relative/../my workspaces"), filepath.Join(base, "chosen workspaces")} {
		got, err := prepareEvalWorkRoot(path, func(string) (string, bool, error) {
			t.Fatal("explicit work root must bypass runtime resolver")
			return "", false, nil
		})
		if err != nil || got != path {
			t.Fatalf("explicit work root = %q, %v; want unchanged %q", got, err, path)
		}
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 0 {
		t.Fatalf("explicit path preparation wrote files: %v, %v", entries, err)
	}
}

func TestEvalDefaultWorkRootRejectsResolverFailureWithoutWrites(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)
	resolveErr := errors.New("runtime data unavailable")
	got, err := prepareEvalWorkRoot("", func(flagValue string) (string, bool, error) {
		if flagValue != "" {
			t.Fatalf("runtime flag = %q", flagValue)
		}
		return "", false, resolveErr
	})
	if got != "" || !errors.Is(err, resolveErr) {
		t.Fatalf("resolution failure = %q, %v", got, err)
	}
	for _, root := range []string{"", "relative-data"} {
		got, err = prepareEvalWorkRoot("", func(string) (string, bool, error) {
			return root, false, nil
		})
		if got != "" || err == nil {
			t.Fatalf("invalid runtime root %q accepted: %q, %v", root, got, err)
		}
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed resolution fell back to cwd: %v, %v", entries, err)
	}
}

func TestEvalDefaultWorkRootRejectsBlockedPortableParents(t *testing.T) {
	for _, component := range []string{"data", "eval", "workspaces"} {
		t.Run(component, func(t *testing.T) {
			base := t.TempDir()
			t.Chdir(base)
			dataRoot := filepath.Join(base, "data")
			blocker := dataRoot
			if component == "eval" {
				blocker = filepath.Join(dataRoot, "eval")
			} else if component == "workspaces" {
				blocker = filepath.Join(dataRoot, "eval", "workspaces")
			}
			if err := os.MkdirAll(filepath.Dir(blocker), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(blocker, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(storage.DataRootEnv, dataRoot)
			got, err := prepareEvalWorkRoot("", storage.ResolveRuntimeDataRoot)
			if got != "" || err == nil {
				t.Fatalf("blocked portable parent accepted: %q, %v", got, err)
			}
			content, readErr := os.ReadFile(blocker)
			if readErr != nil || string(content) != "keep" {
				t.Fatalf("blocker changed: %q, %v", content, readErr)
			}
			if _, err := os.Stat(filepath.Join(base, "supercli-eval")); !os.IsNotExist(err) {
				t.Fatalf("unexpected fallback workspace: %v", err)
			}
		})
	}
}

func TestEvalValidateDoesNotWriteRuntimeDataWorkspacesOrOutput(t *testing.T) {
	for _, tc := range []struct {
		name         string
		blockedData  bool
		explicitWork bool
	}{
		{name: "default"},
		{name: "default-blocked-data", blockedData: true},
		{name: "explicit-work", explicitWork: true},
		{name: "explicit-work-blocked-data", blockedData: true, explicitWork: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			t.Chdir(base)
			if err := os.Mkdir(filepath.Join(base, "fixture"), 0o755); err != nil {
				t.Fatal(err)
			}
			suitePath := filepath.Join(base, "suite.json")
			suite := []byte(`{"id":"portable","name":"Portable validation","tasks":[{"id":"one","name":"One","prompt":"validate only","workspaceFixture":"fixture","verificationCommands":[{"id":"unused","args":["must-not-run"]}]}]}`)
			if err := os.WriteFile(suitePath, suite, 0o600); err != nil {
				t.Fatal(err)
			}
			dataRoot := filepath.Join(base, "data")
			if tc.blockedData {
				if err := os.WriteFile(dataRoot, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv(storage.DataRootEnv, dataRoot)
			workRoot := filepath.Join(base, "unused-workspaces")
			output := filepath.Join(base, "unused-output", "report.json")
			savedArgs := os.Args
			os.Args = []string{"supercli-eval", "--validate", "--suite", suitePath, "--output", output}
			if tc.explicitWork {
				os.Args = append(os.Args, "--work-root", workRoot)
			}
			t.Cleanup(func() { os.Args = savedArgs })
			main()
			for _, path := range []string{workRoot, filepath.Dir(output)} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("validate wrote %q: %v", path, err)
				}
			}
			if tc.blockedData {
				content, err := os.ReadFile(dataRoot)
				if err != nil || string(content) != "keep" {
					t.Fatalf("validate changed blocked data root: %q, %v", content, err)
				}
			} else if _, err := os.Stat(dataRoot); !os.IsNotExist(err) {
				t.Fatalf("validate created runtime data: %v", err)
			}
			if content, err := os.ReadFile(suitePath); err != nil || string(content) != string(suite) {
				t.Fatalf("validate changed suite: %q, %v", content, err)
			}
		})
	}
}
