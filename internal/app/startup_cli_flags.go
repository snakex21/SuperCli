package app

import (
	"flag"
	"fmt"
)

// cliFlags holds every process-level flag SuperCli understands.
// Pointers mirror flag package storage so later stages can still
// mutate draft/credit overrides from TOML when the flag was left
// at its default.
type cliFlags struct {
	Home, DataDir, Config                    string
	ShowVersion, Status, Doctor, ListModels  bool
	Refresh                                  bool
	ModelInfo, Provider, Model, Key, BaseURL string
	Echo, Debug, Resume                      bool
	MaxSession, MaxDay                       int64
	DraftMode, DraftModel, Batch             string
	Coordinator, NoCoordinator, Unsandboxed  bool
}

// parseCLIFlags registers and parses flags. Side effects: sets
// supercliCoordinatorMode from coordinator flags/env.
func parseCLIFlags() cliFlags {
	var f cliFlags
	flag.StringVar(&f.Home, "home", "", "supercli home directory (overrides $SUPERCLI_HOME and cwd)")
	flag.StringVar(&f.DataDir, "data-dir", "", "runtime data directory (overrides $SUPERCLI_DATA_DIR; default: supercli-data beside this executable)")
	flag.BoolVar(&f.ShowVersion, "version", false, "print version and exit")
	flag.BoolVar(&f.Status, "status", false, "print session/credit usage + audit tail and exit")
	flag.BoolVar(&f.Doctor, "doctor", false, "run environment checks and exit")
	flag.BoolVar(&f.ListModels, "list-models", false, "print known model capabilities (with --refresh, re-fetch from the provider)")
	flag.BoolVar(&f.Refresh, "refresh", false, "re-fetch the provider's /v1/models and re-probe unknowns; used with --list-models")
	flag.StringVar(&f.ModelInfo, "model-info", "", "print details for a single model id and exit")
	flag.StringVar(&f.Provider, "provider", "", "LLM provider: openai, responses, anthropic, codex, opencode, or echo (default: openai if SUPERCLI_LLM_API_KEY set, else echo)")
	flag.StringVar(&f.Model, "model", "", "model id (default: env SUPERCLI_LLM_MODEL, then gpt-4o-mini)")
	flag.StringVar(&f.Key, "key", "", "API key (overrides SUPERCLI_LLM_API_KEY)")
	flag.StringVar(&f.BaseURL, "base-url", "", "base URL (overrides SUPERCLI_LLM_BASE_URL)")
	flag.BoolVar(&f.Echo, "echo", false, "force echo provider regardless of env/flags")
	flag.BoolVar(&f.Debug, "debug", false, "verbose logging")
	flag.Int64Var(&f.MaxSession, "max-credits-per-session", 0, "cap total tokens (in+out) per session (0 = no cap)")
	flag.Int64Var(&f.MaxDay, "max-credits-per-day", 0, "cap total tokens (in+out) per UTC day (0 = no cap)")
	flag.StringVar(&f.DraftMode, "draft-mode", "off", "F11 draft mode: off|always|balanced|critical (default off; opt-in, requires --draft-model)")
	flag.StringVar(&f.DraftModel, "draft-model", "", "F11 draft model id (required to enable F11; no auto-pick)")
	flag.StringVar(&f.Config, "config", "", "path to config.toml override")
	flag.StringVar(&f.Batch, "batch", "", "F33: run prompt without TUI, output to stdout and exit")
	flag.BoolVar(&f.Resume, "resume", false, "resume the most recent session on startup")
	flag.BoolVar(&f.Coordinator, "coordinator", false, "run main loop as a lightweight coordinator that delegates code work to isolated task workers (default on)")
	flag.BoolVar(&f.NoCoordinator, "no-coordinator", false, "disable default coordinator mode and expose the normal tool set directly to the main loop")
	flag.BoolVar(&f.Unsandboxed, "allow-all", false, "grant full filesystem access — file operations can reach any directory (sensitive system paths still blocked); same as allow_all = true in config.toml")
	flag.Usage = usage
	flag.Parse()

	supercliCoordinatorMode = true
	if f.NoCoordinator || envFalsey("SUPERCLI_COORDINATOR") {
		supercliCoordinatorMode = false
	}
	if f.Coordinator || envTruthy("SUPERCLI_COORDINATOR") {
		supercliCoordinatorMode = true
	}
	return f
}

func printVersion() {
	fmt.Println("supercli", version)
}
