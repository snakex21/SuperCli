package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"supercli/internal/storage"
)

func TestProbeDefaultLogUsesRuntimeDataRootAndAppendsJSONL(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)
	dataRoot := filepath.Join(base, "instance", "data")
	workspace := filepath.Join(base, "project")
	t.Setenv(storage.DataRootEnv, dataRoot)
	t.Setenv(storage.HomeEnv, workspace)
	records := []requestRecord{
		{Time: "2026-10-09T12:00:00Z", Method: "POST", Path: "/v1/chat/completions", Query: "test=1", Status: 200, Body: "synthetic\nbody"},
		{Time: "2026-10-09T12:01:00Z", Method: "POST", Path: "/v1/chat/completions", Status: 0, Body: "{}"},
	}
	var want []byte
	wantPath := filepath.Join(dataRoot, "logs", "wire.jsonl")
	for _, record := range records {
		record.BodyLen = len(record.Body)
		f, gotPath, err := openProbeLog("", storage.ResolveRuntimeDataRoot)
		if err != nil {
			t.Fatal(err)
		}
		if gotPath != wantPath {
			f.Close()
			t.Fatalf("log path = %q, want %q", gotPath, wantPath)
		}
		line, err := json.Marshal(record)
		if err != nil {
			f.Close()
			t.Fatal(err)
		}
		line = append(line, '\n')
		_, writeErr := f.Write(line)
		syncErr := f.Sync()
		closeErr := f.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			t.Fatalf("append log: write=%v sync=%v close=%v", writeErr, syncErr, closeErr)
		}
		want = append(want, line...)
	}
	got, err := os.ReadFile(wantPath)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("JSONL append changed records: %q, %v; want %q", got, err, want)
	}
	for _, path := range []string{workspace, filepath.Join(base, "wire.jsonl")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("default log wrote outside runtime data at %q: %v", path, err)
		}
	}
}

func TestProbeExplicitLogPreservesPathAndBypassesResolver(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)
	for _, path := range []string{"custom wire.jsonl", filepath.Join(base, "absolute wire.jsonl")} {
		if err := os.WriteFile(path, []byte("existing\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		f, gotPath, err := openProbeLog(path, func(string) (string, bool, error) {
			t.Fatal("explicit log must bypass runtime resolver")
			return "", false, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if gotPath != path {
			f.Close()
			t.Fatalf("explicit path = %q, want unchanged %q", gotPath, path)
		}
		_, writeErr := f.WriteString("next\n")
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatalf("append explicit log: %v, %v", writeErr, closeErr)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != "existing\nnext\n" {
			t.Fatalf("explicit log append = %q, %v", got, err)
		}
	}
}

func TestProbeExplicitLogDoesNotCreateMissingParents(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)
	path := filepath.FromSlash("missing/wire.jsonl")
	f, got, err := openProbeLog(path, func(string) (string, bool, error) {
		t.Fatal("explicit log must bypass runtime resolver")
		return "", false, nil
	})
	if f != nil || got != "" || err == nil {
		if f != nil {
			f.Close()
		}
		t.Fatalf("missing explicit parent accepted: %v, %q, %v", f, got, err)
	}
	if _, err := os.Stat(filepath.Join(base, "missing")); !os.IsNotExist(err) {
		t.Fatalf("created explicit log parent: %v", err)
	}
}

func TestProbeDefaultLogRejectsResolverFailureWithoutWrites(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)
	resolveErr := errors.New("runtime data unavailable")
	f, got, err := openProbeLog("", func(flagValue string) (string, bool, error) {
		if flagValue != "" {
			t.Fatalf("runtime flag = %q", flagValue)
		}
		return "", false, resolveErr
	})
	if f != nil || got != "" || !errors.Is(err, resolveErr) {
		t.Fatalf("resolution failure = %v, %q, %v", f, got, err)
	}
	for _, root := range []string{"", "relative-data"} {
		f, got, err = openProbeLog("", func(string) (string, bool, error) {
			return root, false, nil
		})
		if f != nil || got != "" || err == nil {
			t.Fatalf("invalid runtime root %q accepted: %v, %q, %v", root, f, got, err)
		}
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed resolution fell back to cwd: %v, %v", entries, err)
	}
}

func TestProbeDefaultLogRejectsBlockedPortableParentsAndFile(t *testing.T) {
	for _, component := range []string{"data", "logs", "wire.jsonl"} {
		t.Run(component, func(t *testing.T) {
			base := t.TempDir()
			t.Chdir(base)
			dataRoot := filepath.Join(base, "data")
			blocker := dataRoot
			if component == "logs" {
				blocker = filepath.Join(dataRoot, "logs")
			} else if component == "wire.jsonl" {
				blocker = filepath.Join(dataRoot, "logs", "wire.jsonl")
			}
			if err := os.MkdirAll(filepath.Dir(blocker), 0o755); err != nil {
				t.Fatal(err)
			}
			if component == "wire.jsonl" {
				if err := os.Mkdir(blocker, 0o755); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(blocker, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(storage.DataRootEnv, dataRoot)
			f, got, err := openProbeLog("", storage.ResolveRuntimeDataRoot)
			if f != nil || got != "" || err == nil {
				if f != nil {
					f.Close()
				}
				t.Fatalf("blocked portable log accepted: %v, %q, %v", f, got, err)
			}
			if component != "wire.jsonl" {
				content, err := os.ReadFile(blocker)
				if err != nil || string(content) != "keep" {
					t.Fatalf("blocker changed: %q, %v", content, err)
				}
			}
			if _, err := os.Stat(filepath.Join(base, "wire.jsonl")); !os.IsNotExist(err) {
				t.Fatalf("unexpected cwd fallback log: %v", err)
			}
		})
	}
}
