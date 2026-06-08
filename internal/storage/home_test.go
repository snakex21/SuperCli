package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveHome_FlagWins(t *testing.T) {
	t.Setenv(HomeEnv, "") // make sure env does not leak in
	dir := t.TempDir()
	got, err := ResolveHome(dir)
	if err != nil {
		t.Fatalf("ResolveHome(flag): %v", err)
	}
	abs, _ := filepath.Abs(dir)
	if got != abs {
		t.Fatalf("ResolveHome(flag) = %q, want %q", got, abs)
	}
}

func TestResolveHome_EnvWhenNoFlag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(HomeEnv, dir)
	got, err := ResolveHome("")
	if err != nil {
		t.Fatalf("ResolveHome(\"\"): %v", err)
	}
	abs, _ := filepath.Abs(dir)
	if got != abs {
		t.Fatalf("ResolveHome(\"\") = %q, want %q", got, abs)
	}
}

func TestResolveHome_CwdFallback(t *testing.T) {
	t.Setenv(HomeEnv, "")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	got, err := ResolveHome("")
	if err != nil {
		t.Fatalf("ResolveHome(\"\"): %v", err)
	}
	abs, _ := filepath.Abs(cwd)
	if got != abs {
		t.Fatalf("ResolveHome(\"\") = %q, want %q", got, abs)
	}
}

func TestResolveHome_RelativePathIsAbsolutized(t *testing.T) {
	t.Setenv(HomeEnv, "")
	cwd, _ := os.Getwd()
	got, err := ResolveHome(".")
	if err != nil {
		t.Fatalf("ResolveHome(.): %v", err)
	}
	abs, _ := filepath.Abs(cwd)
	if got != abs {
		t.Fatalf("ResolveHome(.) = %q, want %q", got, abs)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("expected absolute path, got %q", got)
	}
}

func TestResolveHome_FlagBeatsEnv(t *testing.T) {
	flagDir := t.TempDir()
	envDir := t.TempDir()
	t.Setenv(HomeEnv, envDir)
	got, err := ResolveHome(flagDir)
	if err != nil {
		t.Fatalf("ResolveHome: %v", err)
	}
	abs, _ := filepath.Abs(flagDir)
	if got != abs {
		t.Fatalf("flag lost priority: got %q, want %q", got, abs)
	}
}

func TestDataDir(t *testing.T) {
	home := t.TempDir()
	got := DataDir(home)
	want := filepath.Join(home, DataDirName)
	if got != want {
		t.Fatalf("DataDir = %q, want %q", got, want)
	}
}

func TestEnsureDataDir_CreatesMissing(t *testing.T) {
	home := t.TempDir()
	dir, err := EnsureDataDir(home)
	if err != nil {
		t.Fatalf("EnsureDataDir: %v", err)
	}
	want := filepath.Join(home, DataDirName)
	if dir != want {
		t.Fatalf("EnsureDataDir returned %q, want %q", dir, want)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected %q to be a directory", dir)
	}
}

func TestEnsureDataDir_Idempotent(t *testing.T) {
	home := t.TempDir()
	if _, err := EnsureDataDir(home); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := EnsureDataDir(home); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

func TestEnsureDataDir_CreatesNested(t *testing.T) {
	parent := t.TempDir()
	nested := filepath.Join(parent, "deep", "nest")
	if _, err := EnsureDataDir(nested); err != nil {
		t.Fatalf("EnsureDataDir nested: %v", err)
	}
	info, err := os.Stat(filepath.Join(nested, DataDirName))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory")
	}
}

func TestEnsureDataDir_EmptyHome(t *testing.T) {
	if _, err := EnsureDataDir(""); err == nil {
		t.Fatalf("expected error on empty home, got nil")
	}
}

func TestHomeExists_True(t *testing.T) {
	dir := t.TempDir()
	ok, err := HomeExists(dir)
	if err != nil {
		t.Fatalf("HomeExists: %v", err)
	}
	if !ok {
		t.Fatalf("expected true for existing dir")
	}
}

func TestHomeExists_False(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope")
	ok, err := HomeExists(missing)
	if err != nil {
		t.Fatalf("HomeExists: %v", err)
	}
	if ok {
		t.Fatalf("expected false for missing dir")
	}
}

func TestHomeExists_NotADirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	ok, err := HomeExists(file)
	if err != nil {
		t.Fatalf("HomeExists: %v", err)
	}
	if ok {
		t.Fatalf("expected false for a file, got true")
	}
}
