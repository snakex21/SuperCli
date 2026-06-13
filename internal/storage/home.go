// Package storage resolves the SuperCli home directory and ensures the
// on-disk data layout exists. It is the single source of truth for where
// the CLI is allowed to write.
//
// Golden rule: SuperCli never writes to %APPDATA%, ~/.config, or any
// user-global location without explicit consent. The default home is the
// current working directory, which makes every run sandboxed by design.
package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

// DataDirName is the subdirectory inside Home that holds the database,
// memory store, sessions and skills. Hidden so the project tree stays
// visually clean.
const DataDirName = ".supercli"

// DBFileName is the SQLite file inside DataDir.
const DBFileName = "supercli.db"

// HomeEnv is the environment variable that overrides the default
// (cwd-based) home. Lower-case on purpose: this is a developer-facing
// knob, not a user setting, so we keep the API surface small.
const HomeEnv = "SUPERCLI_HOME"

// ResolveHome picks the home directory in this priority order:
//
//  1. flagValue (the --home CLI flag), if non-empty.
//  2. $SUPERCLI_HOME, if set and non-empty.
//  3. The current working directory.
//
// Relative paths are always converted to absolute paths so downstream
// code never has to second-guess its CWD. An empty flagValue is
// treated as "not provided", not as "use the current directory".
func ResolveHome(flagValue string) (string, error) {
	candidate, source, err := pickCandidate(flagValue)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve home from %s (%q): %w", source, candidate, err)
	}
	return abs, nil
}

func pickCandidate(flagValue string) (string, string, error) {
	if flagValue != "" {
		return flagValue, "--home flag", nil
	}
	if env := os.Getenv(HomeEnv); env != "" {
		return env, HomeEnv + " env var", nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("get working directory: %w", err)
	}
	return cwd, "working directory", nil
}

// DataDir returns the absolute path of the per-project data directory.
// It does not touch the filesystem.
func DataDir(home string) string {
	return filepath.Join(home, DataDirName)
}

// EnsureDataDir creates the per-project data directory if it does not
// already exist. The returned path is absolute.
//
// MkdirAll is used so the call is safe to invoke repeatedly and so it
// tolerates a missing parent (e.g. when --home points at a brand new
// project). Mode 0o755 is intentional: read-only is *not* the default
// because the CLI must be able to write the database, the memory
// store, and the session log.
func EnsureDataDir(home string) (string, error) {
	if home == "" {
		return "", fmt.Errorf("EnsureDataDir: home is empty")
	}
	dir := DataDir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create data dir %q: %w", dir, err)
	}
	return dir, nil
}

// PortableDataDirName is the name of the data directory that sits
// NEXT TO THE EXECUTABLE in the default (portable) mode. SuperCli is
// always portable: every piece of CLI state (config.toml, databases,
// memory, sessions, auth tokens, caches, logs) lives in this single
// directory beside the binary — never in the user profile.
const PortableDataDirName = "supercli-data"

// PortableDataRoot returns <dir-of-executable>/supercli-data.
// Symlinks on the executable path are resolved so a symlinked binary
// still finds its real data directory. When os.Executable fails the
// current working directory is the fallback base.
func PortableDataRoot() string {
	base := ""
	if exe, err := os.Executable(); err == nil && exe != "" {
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil && resolved != "" {
			exe = resolved
		}
		base = filepath.Dir(exe)
	}
	if base == "" {
		if cwd, err := os.Getwd(); err == nil {
			base = cwd
		} else {
			base = "."
		}
	}
	if abs, err := filepath.Abs(base); err == nil {
		base = abs
	}
	return filepath.Join(base, PortableDataDirName)
}

// ResolveDataRoot is the SINGLE source of truth for where SuperCli
// writes its data. Priority:
//
//  1. --home flag       → <flag>/.supercli   (explicit override)
//  2. $SUPERCLI_HOME    → <env>/.supercli    (explicit override)
//  3. default (portable) → <exe dir>/supercli-data
//
// portable reports whether the default exe-relative location was
// chosen (i.e. no explicit override) — callers use it to gate the
// one-time ~/.supercli migration.
func ResolveDataRoot(flagValue string) (root string, portable bool, err error) {
	if flagValue != "" || os.Getenv(HomeEnv) != "" {
		home, herr := ResolveHome(flagValue)
		if herr != nil {
			return "", false, herr
		}
		return DataDir(home), false, nil
	}
	return PortableDataRoot(), true, nil
}

// EnsureDir creates dir (and parents) if missing.
func EnsureDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("EnsureDir: empty path")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create data dir %q: %w", dir, err)
	}
	return nil
}

// HomeExists reports whether the given home directory exists on disk.
// Used by startup code to decide whether to print a "first run" banner.
func HomeExists(home string) (bool, error) {
	info, err := os.Stat(home)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return info.IsDir(), nil
}
