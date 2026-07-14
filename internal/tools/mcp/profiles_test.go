package mcp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestObscuraPortableSmoke exercises the real portable binary when explicitly
// requested. It stays skipped in ordinary/CI runs because the binary is user
// data rather than a repository dependency.
func TestObscuraPortableSmoke(t *testing.T) {
	dataDir := os.Getenv("SUPERCLI_OBSCURA_SMOKE")
	if dataDir == "" {
		t.Skip("set SUPERCLI_OBSCURA_SMOKE to a supercli-data directory")
	}
	configs, packages, err := LoadWorkspace(dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg, ok := configs["obscura"]
	if !ok {
		t.Fatalf("obscura not discovered; packages=%+v", packages)
	}
	server := &Server{Name: "obscura", Config: cfg}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	if status := server.Status(); !status.Running || status.Tools == 0 {
		t.Fatalf("status = %+v", status)
	}
}

func TestSeedDetectedPortableProfilesAddsObscuraWithoutStartingIt(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "supercli-data")
	pkgDir := filepath.Join(dataDir, PortableDirName, "obscura")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	command := "obscura"
	if runtime.GOOS == "windows" {
		command += ".exe"
	}
	if err := os.WriteFile(filepath.Join(pkgDir, command), []byte("not executed"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := SeedDetectedPortableProfiles(dataDir); err != nil {
		t.Fatal(err)
	}
	packages, err := DiscoverPortable(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 || packages[0].Name != "obscura" || !packages[0].Available {
		t.Fatalf("packages = %+v", packages)
	}
	if got := packages[0].Resolved.Args; len(got) != 1 || got[0] != "mcp" {
		t.Fatalf("args = %v", got)
	}
}

func TestSeedDetectedPortableProfilesPreservesCustomManifest(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "supercli-data")
	pkgDir := filepath.Join(dataDir, PortableDirName, "obscura")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(pkgDir, ManifestName)
	const custom = "name = \"custom-obscura\"\ncommand = \"elsewhere\"\n"
	if err := os.WriteFile(manifestPath, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SeedDetectedPortableProfiles(dataDir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != custom {
		t.Fatalf("custom manifest overwritten: %q", got)
	}
}
