// Command supercli-web launches the SuperCli web GUI: a local,
// dark-themed desktop-style front-end over the same agent engine the
// TUI drives. It is a separate binary so the default `supercli` stays
// a pure terminal tool; this one adds an HTTP server + app-mode
// window while sharing every core package.
//
// Usage:
//
//	supercli-web [--home PATH] [--data-dir PATH] [--provider P] [--model M]
//	             [--key K] [--base-url U] [--addr ADDR] [--no-window]
//	             [--allow-remote]
//
// --home/SUPERCLI_HOME select only the workspace. Application state defaults
// to supercli-data beside this executable and can be changed only with the
// explicit --data-dir/SUPERCLI_DATA_DIR override.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"supercli/internal/storage"
	"supercli/internal/storage/memory"
	"supercli/internal/system/config"
	"supercli/internal/system/uilang"
	"supercli/internal/tools/sandbox"
	"supercli/internal/webgui"
)

func main() {
	detachAccidentalConsole()
	crashDataDir := storage.PortableDataRoot()
	defer func() {
		if recovered := recover(); recovered != nil {
			writeWebCrash(crashDataDir, recovered)
		}
	}()
	run(&crashDataDir)
}

func run(crashDataDir *string) {
	homeFlag := flag.String("home", "", "supercli home directory (overrides $SUPERCLI_HOME and cwd)")
	workspaceFlag := flag.String("workspace", "", "active workspace directory without changing the portable data/config location")
	dataDirFlag := flag.String("data-dir", "", "runtime data directory (overrides $SUPERCLI_DATA_DIR; default: supercli-data beside this executable)")
	providerFlag := flag.String("provider", "", "LLM provider: openai, responses, anthropic, opencode, codex, or echo")
	modelFlag := flag.String("model", "", "model id")
	keyFlag := flag.String("key", "", "API key (overrides SUPERCLI_LLM_API_KEY)")
	baseFlag := flag.String("base-url", "", "base URL (overrides SUPERCLI_LLM_BASE_URL)")
	echoFlag := flag.Bool("echo", false, "force echo provider")
	addrFlag := flag.String("addr", "", "listen address (default 127.0.0.1:0, an OS-assigned port)")
	noWindowFlag := flag.Bool("no-window", false, "do not open a browser window; serve only")
	uiDirFlag := flag.String("ui-dir", "", "serve a custom static UI directory instead of the embedded SuperCli front-end")
	appNameFlag := flag.String("app-name", "", "desktop window name (for branded launchers)")
	appProfileFlag := flag.String("app-profile", "", "single-instance profile (for branded launchers)")
	iconFlag := flag.String("icon", "", "path to a Windows .ico file for the native window")
	requireUIContractFlag := flag.Int("require-ui-contract", 0, "minimum shared UI contract required by the launcher")
	parentPIDFlag := flag.Int("parent-pid", 0, "exit when this launcher process exits")
	allowRemoteFlag := flag.Bool("allow-remote", false, "allow token-protected non-loopback access")
	allowAllFlag := flag.Bool("allow-all", false, "allow file and search tools to use absolute paths outside the active workspace")
	debugFlag := flag.Bool("debug", false, "verbose logging")
	flag.Parse()

	uiFS, appName, appProfile := bundledUI()
	if strings.TrimSpace(*appNameFlag) != "" {
		appName = strings.TrimSpace(*appNameFlag)
	}
	if strings.TrimSpace(*appProfileFlag) != "" {
		appProfile = strings.TrimSpace(*appProfileFlag)
	}
	if err := validateUIContract(*requireUIContractFlag); err != nil {
		fmt.Fprintln(os.Stderr, "supercli-web: UI compatibility:", err)
		os.Exit(2)
	}
	monitorParent(*parentPIDFlag)
	if !*noWindowFlag {
		releaseInstance, alreadyRunning, instanceErr := claimSingleInstance(appProfile)
		if instanceErr != nil {
			fatal("single instance", instanceErr)
		}
		if alreadyRunning {
			notifyAlreadyRunning(appName, startupUILanguage(*homeFlag, *dataDirFlag))
			return
		}
		defer releaseInstance()
	}

	home, err := storage.ResolveHome(*homeFlag)
	if err != nil {
		fatal("resolve home", err)
	}
	dataDir, _, err := storage.ResolveRuntimeDataRoot(*dataDirFlag)
	if err != nil {
		fatal("resolve data dir", err)
	}
	if err := storage.EnsureDir(dataDir); err != nil {
		fatal("ensure data dir", err)
	}
	if err := webgui.ApplyPendingDataImport(dataDir); err != nil {
		failed := filepath.Join(dataDir, fmt.Sprintf("pending-data-import.failed-%d.json", time.Now().Unix()))
		_ = os.Rename(filepath.Join(dataDir, "pending-data-import.json"), failed)
		_ = os.MkdirAll(filepath.Join(dataDir, "logs"), 0o755)
		_ = os.WriteFile(filepath.Join(dataDir, "logs", "data-import-error.log"), []byte(err.Error()+"\n"), 0o600)
	}
	*crashDataDir = dataDir
	if logFile := initWebLog(dataDir); logFile != nil {
		defer logFile.Close()
	}

	// Match CLI project startup semantics: when no explicit --home or
	// SUPERCLI_HOME is provided, the active named workspace becomes the web
	// sandbox root. Without this, selecting projects in the UI only changed
	// workspace.json and never affected the actual web engine.
	workspace := memory.LoadWorkspace(dataDir)
	activeProject, hasActiveProject := workspace.ActiveProject()
	activeProjectApplied := false
	if *workspaceFlag != "" {
		workspaceHome, workspaceErr := filepath.Abs(*workspaceFlag)
		if workspaceErr != nil {
			fatal("resolve workspace", workspaceErr)
		}
		if fi, statErr := os.Stat(workspaceHome); statErr != nil || !fi.IsDir() {
			if statErr != nil {
				fatal("open workspace", statErr)
			}
			fatal("open workspace", fmt.Errorf("not a directory: %s", workspaceHome))
		}
		home = workspaceHome
	} else if hasActiveProject && *homeFlag == "" && os.Getenv(storage.HomeEnv) == "" {
		if fi, statErr := os.Stat(activeProject.Path); statErr == nil && fi.IsDir() {
			home = activeProject.Path
			activeProjectApplied = true
		}
	}

	if *echoFlag {
		*keyFlag = ""
		*providerFlag = config.ProviderEcho
	}
	// Resolve project config against the effective sandbox root, not merely
	// the process cwd. This keeps web project switching aligned with the file
	// tools, file browser and displayed workspace.
	tomlCfg, tomlErr := config.ResolveConfig(dataDir, home, "")
	if tomlErr != nil {
		// ResolveConfig preserves any valid lower-priority layer. Keep using it
		// so one malformed project override cannot silently turn a configured
		// web app into the test echo provider. The NestCafe runtime additionally
		// rejects echo chat requests, so internal prompt add-ons can never leak.
		log.Printf("config: %v (using valid lower-priority settings)", tomlErr)
	}
	uiLanguage, languageErr := config.EnsureLanguage(dataDir, home, tomlCfg.Language)
	if languageErr != nil {
		log.Printf("language: %v (using %s for this run)", languageErr, uiLanguage)
	}
	tomlCfg.Language = uiLanguage
	cfg, err := config.Load(config.FlagOverrides{
		Provider: *providerFlag,
		APIKey:   *keyFlag,
		BaseURL:  *baseFlag,
		Model:    *modelFlag,
		Debug:    boolPtr(*debugFlag),
	})
	if err != nil {
		fatal("load config", err)
	}
	if !*echoFlag {
		config.ApplyTomlToConfig(&cfg, tomlCfg)
		// ApplyTomlToConfig resolves a named TOML provider as one
		// type/base/key bundle. Restore explicit env and flag values after
		// that merge so command-line startup overrides remain authoritative.
		config.EnvOverrideConfig(&cfg)
		if *providerFlag != "" {
			cfg.Provider = *providerFlag
		}
		if *keyFlag != "" {
			cfg.APIKey = *keyFlag
		}
		if *baseFlag != "" {
			cfg.BaseURL = *baseFlag
		}
		if *modelFlag != "" {
			cfg.Model = *modelFlag
		}
		providerExplicit := *providerFlag != "" || os.Getenv("SUPERCLI_LLM_PROVIDER") != ""
		baseExplicit := *baseFlag != "" || os.Getenv("SUPERCLI_LLM_BASE_URL") != ""
		keyExplicit := *keyFlag != "" || os.Getenv("SUPERCLI_LLM_API_KEY") != ""
		modelExplicit := *modelFlag != "" || os.Getenv("SUPERCLI_LLM_MODEL") != ""
		// config.Load normalizes empty model to "no model" before TOML is
		// applied. For web GUI startup, the saved model must still win when
		// no explicit --model/env override was provided.
		if !modelExplicit && tomlCfg.DefaultModel != "" {
			cfg.Model = tomlCfg.DefaultModel
		}
		// The web GUI keeps its OWN active model in webgui-settings.json
		// (written on every picker switch). It outranks the CLI's
		// default_model from config.toml so the two front-ends stay
		// independent: picking a model in the browser neither writes to
		// config.toml nor is it clobbered by a later CLI /model swap.
		// Explicit --model / env still wins over both.
		if !modelExplicit && !providerExplicit {
			if lm, lp := webgui.LastModel(dataDir); lm != "" {
				if p, ok := config.ResolveProviderConf(dataDir, tomlCfg, lp); ok {
					// Restore the remembered model only when its provider can be
					// resolved. This prevents stale UI state from pairing a model
					// with unrelated credentials.
					cfg.Model = lm
					cfg.Provider = p.Type
					if !baseExplicit {
						cfg.BaseURL = p.BaseURL
					}
					if !keyExplicit {
						cfg.APIKey = p.APIKey
					}
				}
			}
		}
		// Active project's preferred model/provider: more specific than the web
		// last-model setting, but explicit flags/env still win.
		if activeProjectApplied {
			if activeProject.Provider != "" && !providerExplicit {
				if p, ok := config.ResolveProviderConf(dataDir, tomlCfg, activeProject.Provider); ok {
					cfg.Provider = p.Type
					if !baseExplicit {
						cfg.BaseURL = p.BaseURL
					}
					if !keyExplicit {
						cfg.APIKey = p.APIKey
					}
				}
			}
			if activeProject.Model != "" && !modelExplicit {
				cfg.Model = activeProject.Model
			}
			if err := cfg.Normalize(); err != nil {
				log.Printf("project %q: normalize config: %v (ignored)", activeProject.Name, err)
			}
		}
		// Process-global LLM settings from config.toml — cache_prompt,
		// [sampling], reasoning_effort, thinking — applied to every
		// provider this process builds. Same call as the TUI and batch
		// paths; the web GUI used to set only sampling and reasoning
		// effort, so a configured cache_prompt did nothing until the
		// user toggled the knob in the UI.
		config.ApplyLLMGlobals(tomlCfg, cfg.Temperature)
		if err := cfg.Normalize(); err != nil {
			fatal("normalize config", err)
		}
	}
	allowAll := resolveAllowAll(appProfile, *allowAllFlag, tomlErr == nil && tomlCfg.AllowAll, os.Getenv("SUPERCLI_ALLOW_ALL"))
	sandbox.SetUnsandboxed(allowAll)

	eng, err := webgui.NewEngine(cfg, home, dataDir)
	if err != nil {
		fatal("build engine", err)
	}
	eng.SetAppProfile(appProfile)
	eng.SetExplicitEcho(*echoFlag)
	eng.RefreshPricingAsync()

	if *uiDirFlag != "" {
		uiFS = nil
	}
	if err := webgui.Run(eng, webgui.RunOptions{
		Addr:        *addrFlag,
		AllowRemote: *allowRemoteFlag,
		NoWindow:    *noWindowFlag,
		UIRoot:      *uiDirFlag,
		UIFS:        uiFS,
		AppName:     appName,
		IconPath:    *iconFlag,
	}); err != nil {
		fatal("run", err)
	}
}

func resolveAllowAll(appProfile string, flagEnabled, configured bool, environment string) bool {
	if strings.EqualFold(strings.TrimSpace(appProfile), "nestcafe") {
		return true
	}
	if flagEnabled || configured {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// fatal prints a context-tagged error and exits non-zero.
func fatal(ctx string, err error) {
	fmt.Fprintf(os.Stderr, "supercli-web: %s: %v\n", ctx, err)
	log.Fatalf("%s: %v", ctx, err)
}

// boolPtr returns a pointer to b; used for the optional Debug
// override which must distinguish unset from false.
func boolPtr(b bool) *bool { return &b }

// startupUILanguage reads the persisted UI language before the data dir is
// set up, so the single-instance notice — the one message shown before the
// engine exists — speaks the language the rest of the app does. Every step is
// read-only and falls back to host detection, because a notice must never be
// the thing that fails startup.
func startupUILanguage(homeFlag, dataDirFlag string) string {
	home, err := storage.ResolveHome(homeFlag)
	if err != nil {
		return uilang.Resolve("")
	}
	dataDir, _, err := storage.ResolveRuntimeDataRoot(dataDirFlag)
	if err != nil {
		return uilang.Resolve("")
	}
	resolved, _ := config.ResolveConfig(dataDir, home, "")
	return uilang.Resolve(resolved.Language)
}

func validateUIContract(required int) error {
	if required < 0 || required > webgui.UIContractVersion {
		return fmt.Errorf("launcher requires UI contract %d, engine provides %d", required, webgui.UIContractVersion)
	}
	return nil
}

func initWebLog(dataDir string) *os.File {
	logsDir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(logsDir, "supercli-web.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	log.SetOutput(f)
	return f
}

func writeWebCrash(dataDir string, recovered any) {
	path := filepath.Join(dataDir, "logs", "supercli-web-crash.log")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "=== CRASH %s ===\npanic: %v\n%s\n\n", time.Now().Format(time.RFC3339Nano), recovered, debug.Stack())
	_ = f.Sync()
}
