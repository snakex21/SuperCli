package mcp

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPortablePackageSurvivesWorkspaceRelocation(t *testing.T) {
	base := t.TempDir()
	firstRoot := filepath.Join(base, "first")
	firstData := filepath.Join(firstRoot, "supercli-data")
	pkgDir := filepath.Join(firstData, PortableDirName, "demo")
	if err := os.MkdirAll(filepath.Join(pkgDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	commandName := "server"
	if runtime.GOOS == "windows" {
		commandName += ".exe"
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "bin", commandName), []byte("placeholder"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `schema = 1
name = "portable-demo"
description = "relocation test"
command = "bin/` + commandName + `"
args = ["--asset", "${MCP_DIR}/assets/file.txt", "--root", "${SUPERCLI_DIR}"]
tags = ["demo", "portable"]
platforms = ["` + runtime.GOOS + `/` + runtime.GOARCH + `"]
`
	if err := os.WriteFile(filepath.Join(pkgDir, ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	configs, packages, err := LoadWorkspace(firstData, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 || !packages[0].Available {
		t.Fatalf("packages = %+v", packages)
	}
	if !strings.HasPrefix(configs["portable-demo"].Command, firstRoot) {
		t.Fatalf("first command = %q, want below %q", configs["portable-demo"].Command, firstRoot)
	}

	secondRoot := filepath.Join(base, "second")
	if err := os.Rename(firstRoot, secondRoot); err != nil {
		t.Fatal(err)
	}
	secondData := filepath.Join(secondRoot, "supercli-data")
	configs, packages, err = LoadWorkspace(secondData, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := configs["portable-demo"]
	if !packages[0].Available || !strings.HasPrefix(got.Command, secondRoot) {
		t.Fatalf("relocated command = %q; package = %+v", got.Command, packages[0])
	}
	for _, value := range append([]string{got.Command}, got.Args...) {
		if strings.Contains(value, firstRoot) {
			t.Fatalf("stale absolute path survived relocation: %q", value)
		}
	}
}

func TestPortablePackageReportsMissingHostRequirement(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "supercli-data")
	pkgDir := filepath.Join(dataDir, PortableDirName, "blender")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	command := "bridge"
	if runtime.GOOS == "windows" {
		command += ".exe"
	}
	if err := os.WriteFile(filepath.Join(pkgDir, command), []byte("placeholder"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `schema = 1
name = "blender"
command = "` + command + `"

[[requires]]
name = "Blender"
candidates = ["definitely-missing-supercli-test-host"]
`
	if err := os.WriteFile(filepath.Join(pkgDir, ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	packages, err := DiscoverPortable(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 || packages[0].Available || !strings.Contains(packages[0].Error, "Blender") {
		t.Fatalf("package diagnostics = %+v", packages)
	}
}

func TestExplicitConfigOverridesPortablePackage(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "supercli-data")
	pkgDir := filepath.Join(dataDir, PortableDirName, "same")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, ManifestName), []byte("name=\"same\"\ncommand=\"missing\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configs, _, err := LoadWorkspace(dataDir, map[string]ServerConfig{
		"same": {Command: "echo", Args: []string{"${DATA_DIR}"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if configs["same"].Command != "echo" || configs["same"].Args[0] != dataDir {
		t.Fatalf("explicit config = %+v", configs["same"])
	}
}
