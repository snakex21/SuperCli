package mcp

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// SeedDetectedPortableProfiles installs tiny manifests for supported MCP
// executables that are already present. It never downloads software and never
// starts a process. This lets a user drop one binary into the portable package
// folder without hand-writing configuration.
func SeedDetectedPortableProfiles(dataDir string) error {
	return seedObscuraProfile(dataDir)
}

func seedObscuraProfile(dataDir string) error {
	pkgDir := filepath.Join(dataDir, PortableDirName, "obscura")
	manifestPath := filepath.Join(pkgDir, ManifestName)
	if _, err := os.Stat(manifestPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	localName := "obscura"
	if runtime.GOOS == "windows" {
		localName += ".exe"
	}
	if !fileExists(filepath.Join(pkgDir, localName)) {
		if _, err := exec.LookPath("obscura"); err != nil {
			return nil
		}
	}
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return err
	}

	const manifest = `schema = 1
name = "obscura"
description = "On-demand browser automation and web inspection through Obscura"
command = "obscura"
args = ["mcp"]
tags = ["browser", "web", "automation", "portable"]
`
	f, err := os.OpenFile(manifestPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString(manifest); err != nil {
		_ = f.Close()
		_ = os.Remove(manifestPath)
		return err
	}
	return f.Close()
}
