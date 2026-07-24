package app

import (
	"fmt"
	"log"
	"os"

	"supercli/internal/storage"
	"supercli/internal/storage/memory"
	"supercli/internal/system/config"
	"supercli/internal/tools/sandbox"
)

// workspaceResolution is the portable-home + project sandbox + data dir.
type workspaceResolution struct {
	Home             string
	DataDir          string
	Cwd              string
	Portable         bool
	ActiveProject    memory.Project
	HasActiveProject bool
	UILanguage       string
	Toml             config.TomlConfig
	TomlErr          error
}

// resolveWorkspace applies --home/--data-dir, legacy migration, project
// selection, and config.toml hierarchy. Sets workingDirNote and may
// enable unsandboxed mode.
func resolveWorkspace(f cliFlags) workspaceResolution {
	var r workspaceResolution

	resolvedHome, err := storage.ResolveHome(f.Home)
	if err != nil {
		fatal("resolve home", err)
	}
	r.Home = resolvedHome
	// workingDirNote is set below, after the TOML + unsandboxed
	// flag are resolved so it reflects the real sandbox state.

	// Workspace and application state are intentionally independent. Opening a
	// project through --home/SUPERCLI_HOME must never make this portable copy
	// read another copy's settings.
	resolvedData, portable, err := storage.ResolveRuntimeDataRoot(f.DataDir)
	if err != nil {
		fatal("resolve data dir", err)
	}
	r.DataDir = resolvedData
	r.Portable = portable

	// One-time migration from the legacy ~/.supercli location.
	if portable {
		if msg, merr := migrateLegacyData(r.DataDir); merr != nil {
			log.Printf("legacy data migration failed: %v (continuing with a fresh %s)", merr, r.DataDir)
		} else if msg != "" {
			fmt.Fprintln(os.Stderr, msg)
		}
	}

	if err := storage.EnsureDir(r.DataDir); err != nil {
		fatalUnwritableDataDir(r.DataDir, portable, err)
	}
	// Verify write permissions — the exe may sit in a read-only
	// location (e.g. Program Files, network drives).
	if err := checkDirWritable(r.DataDir); err != nil {
		fatalUnwritableDataDir(r.DataDir, portable, err)
	}

	// Projects (named workspaces): the active project's directory becomes
	// the agent's sandbox root. Sandbox root is a startup decision (every
	// file tool binds its base dir at construction), so a project is
	// selected here, before any tool wiring. An explicit --home flag or
	// $SUPERCLI_HOME always wins over the active project — project
	// selection only overrides the cwd fallback. The project's optional
	// model/provider are applied further below, after config load.
	workspace := memory.LoadWorkspace(r.DataDir)
	activeProject, hasActiveProject := workspace.ActiveProject()
	r.ActiveProject = activeProject
	r.HasActiveProject = hasActiveProject
	if hasActiveProject && f.Home == "" && os.Getenv(storage.HomeEnv) == "" {
		if fi, statErr := os.Stat(activeProject.Path); statErr == nil && fi.IsDir() {
			r.Home = activeProject.Path
		}
	}

	// F29: resolve config.toml hierarchy.
	// global < project < --config < env < flags.
	cwd, _ := os.Getwd()
	r.Cwd = cwd
	tomlCfg, tomlErr := config.ResolveConfig(r.DataDir, cwd, f.Config)
	if tomlErr != nil {
		log.Printf("config.toml: %v (using defaults)", tomlErr)
	}
	r.TomlErr = tomlErr
	uiLanguage, languageErr := config.EnsureLanguage(r.DataDir, cwd, tomlCfg.Language)
	if languageErr != nil {
		log.Printf("language: %v (using %s for this run)", languageErr, uiLanguage)
	}
	tomlCfg.Language = uiLanguage
	r.UILanguage = uiLanguage
	r.Toml = tomlCfg
	// Apply TOML as defaults (env/flags still win later).
	config.TomlConfigToEnv(tomlCfg)
	// Unsandboxed: flag > env (which TomlConfigToEnv may have set) > default off.
	if f.Unsandboxed || envTruthy("SUPERCLI_ALLOW_ALL") {
		sandbox.SetUnsandboxed(true)
	}
	// State the real sandbox root (the BaseDir file tools enforce) so
	// the model's first file/list call uses the correct path. Set AFTER
	// the unsandboxed decision so it reflects the actual sandbox state.
	if sandbox.IsUnsandboxed() {
		workingDirNote = "Working directory: " + r.Home +
			"\nFull filesystem access is ON (--allow-all). You can read and write files anywhere on the filesystem. Prefer absolute paths."
	} else {
		workingDirNote = "Working directory (file sandbox root): " + r.Home +
			"\nUse this exact path for file and directory operations. Relative paths resolve here; paths must stay inside it."
	}
	return r
}
