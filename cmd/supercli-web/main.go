// Command supercli-web launches the SuperCli web GUI: a local,
// dark-themed desktop-style front-end over the same agent engine the
// TUI drives. It is a separate binary so the default `supercli` stays
// a pure terminal tool; this one adds an HTTP server + app-mode
// window while sharing every core package.
//
// Usage:
//
//	supercli-web [--home PATH] [--provider P] [--model M] [--key K]
//	             [--base-url U] [--addr ADDR] [--no-window] [--allow-remote]
//
// Home/data resolution matches the CLI: --home > $SUPERCLI_HOME > cwd,
// with all state in the portable supercli-data/ directory.
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

	"supercli/internal/llm"
	"supercli/internal/storage"
	"supercli/internal/storage/memory"
	"supercli/internal/system/config"
	"supercli/internal/tools/sandbox"
	"supercli/internal/webgui"
)

func main() {
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
	providerFlag := flag.String("provider", "", "LLM provider: openai, responses, anthropic, opencode, codex, or echo")
	modelFlag := flag.String("model", "", "model id")
	keyFlag := flag.String("key", "", "API key (overrides SUPERCLI_LLM_API_KEY)")
	baseFlag := flag.String("base-url", "", "base URL (overrides SUPERCLI_LLM_BASE_URL)")
	echoFlag := flag.Bool("echo", false, "force echo provider")
	addrFlag := flag.String("addr", "", "listen address (default 127.0.0.1:0, an OS-assigned port)")
	noWindowFlag := flag.Bool("no-window", false, "do not open a browser window; serve only")
	allowRemoteFlag := flag.Bool("allow-remote", false, "allow non-loopback hosts (no auth provided; use with care)")
	allowAllFlag := flag.Bool("allow-all", false, "allow file and search tools to use absolute paths outside the active workspace")
	debugFlag := flag.Bool("debug", false, "verbose logging")
	flag.Parse()

	home, err := storage.ResolveHome(*homeFlag)
	if err != nil {
		fatal("resolve home", err)
	}
	dataDir, _, err := storage.ResolveDataRoot(*homeFlag)
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
	if hasActiveProject && *homeFlag == "" && os.Getenv(storage.HomeEnv) == "" {
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
	if tomlErr == nil && !*echoFlag {
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
		// Restore saved reasoning effort
		if tomlCfg.ReasoningEffort != "" {
			if err := llm.SetReasoningEffort(tomlCfg.ReasoningEffort); err != nil {
				log.Printf("config: reasoning_effort: %v (ignored)", err)
			}
		}
		if err := cfg.Normalize(); err != nil {
			fatal("normalize config", err)
		}
	}
	allowAll := *allowAllFlag || (tomlErr == nil && tomlCfg.AllowAll)
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SUPERCLI_ALLOW_ALL"))) {
	case "1", "true", "yes", "on":
		allowAll = true
	}
	sandbox.SetUnsandboxed(allowAll)

	eng, err := webgui.NewEngine(cfg, home, dataDir)
	if err != nil {
		fatal("build engine", err)
	}
	eng.RefreshPricingAsync()

	if err := webgui.Run(eng, webgui.RunOptions{
		Addr:        *addrFlag,
		AllowRemote: *allowRemoteFlag,
		NoWindow:    *noWindowFlag,
	}); err != nil {
		fatal("run", err)
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
