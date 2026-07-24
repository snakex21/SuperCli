package app

import (
	"log"
	"os"
	"runtime"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/system/config"
	"supercli/internal/system/execution"
)

// platformHint returns OS-specific shell hints so the model uses commands
// appropriate for the current platform.
func platformHint() string {
	switch runtime.GOOS {
	case "windows":
		return "OS: Windows. Use 'dir' instead of 'ls', 'type' instead of 'cat', 'del' instead of 'rm', 'copy' instead of 'cp', 'move' instead of 'mv', backslash paths. For shell commands use cmd /c prefix."
	case "darwin":
		return "OS: macOS. Use standard Unix commands."
	default:
		return "OS: Linux. Use standard Unix commands."
	}
}

func envTruthy(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on", "y":
		return true
	default:
		return false
	}
}

func envFalsey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "0", "false", "no", "off", "n":
		return true
	default:
		return false
	}
}

// These defaults are kept together because they define the runtime execution
// profile applied by both interactive and batch entry points.
const (
	defaultStableToolset = true
	defaultPreflightRepo = true
)

func resolveStableToolset(override *bool) bool {
	return execution.StableToolset(override)
}

func resolvePreflightRepo(override *bool) bool {
	if override != nil {
		return *override
	}
	return defaultPreflightRepo
}

func resolveNavigator(mode string) (enable, auto bool) {
	return execution.Navigator(mode)
}

// resolveTaskWorkerConfig maps task_model onto the provider config used by
// delegated workers. A providerName/model-id reference switches transport;
// an unmatched slash remains part of a bare model id (OpenRouter convention).
func resolveTaskWorkerConfig(tomlCfg config.TomlConfig, cfg config.Config) (config.Config, bool) {
	return resolveModelOverrideConfig(strings.TrimSpace(tomlCfg.TaskModel), "task_model", tomlCfg, cfg)
}

func resolveCompactConfig(tomlCfg config.TomlConfig, cfg config.Config) (config.Config, bool) {
	return resolveModelOverrideConfig(strings.TrimSpace(tomlCfg.CompactModel), "compact_model", tomlCfg, cfg)
}

// resolveModelOverrideConfig implements the shared "model" or
// "provider/model" reference contract used by task_model and compact_model.
// A slash remains part of a bare model ID unless its prefix names a configured
// provider, which preserves OpenRouter-style model identifiers.
func resolveModelOverrideConfig(ref, label string, tomlCfg config.TomlConfig, cfg config.Config) (config.Config, bool) {
	if ref == "" {
		return cfg, false
	}
	override := cfg
	if name, model, found := strings.Cut(ref, "/"); found {
		for _, p := range tomlCfg.Providers {
			if p.Name != name {
				continue
			}
			override.Provider = p.Type
			override.BaseURL = p.BaseURL
			override.APIKey = p.APIKey
			override.Model = model
			if override.Model == "" {
				override.Model = p.Model
			}
			if override.Model == "" {
				log.Printf("%s: %q resolves to no model (provider %q has none configured) — using the main provider", label, ref, name)
				return cfg, false
			}
			if override.Model == cfg.Model && override.BaseURL == cfg.BaseURL {
				return cfg, false
			}
			return override, true
		}
	}
	override.Model = ref
	if override.Model == cfg.Model {
		return cfg, false
	}
	return override, true
}

// resolveNavigatorProvider keeps the navigator away from the main local-model
// KV-cache slot when an already configured side provider is available.
func resolveNavigatorProvider(taskWorker, draft llm.Provider) llm.Provider {
	if taskWorker != nil {
		return taskWorker
	}
	if draft != nil && !strings.Contains(strings.ToLower(draft.Name()), "echo") {
		return draft
	}
	return nil
}

type orchestratorMode uint8

const (
	orchestratorAdaptive orchestratorMode = iota // nil: normal coordinator may delegate when useful
	orchestratorAlways                           // true: restricted coordinator must delegate substantial work
	orchestratorNever                            // false: worker tools are absent from the main agent
)

func resolveOrchestratorMode(override *bool) orchestratorMode {
	if override == nil {
		return orchestratorAdaptive
	}
	if *override {
		return orchestratorAlways
	}
	return orchestratorNever
}

func (m orchestratorMode) hard() bool              { return m == orchestratorAlways }
func (m orchestratorMode) delegationEnabled() bool { return m != orchestratorNever }

// resolveOrchestrator is retained for the loop's hard-orchestrator boolean:
// only the explicit "always" state restricts the coordinator registry.
func resolveOrchestrator(override *bool) bool {
	return resolveOrchestratorMode(override).hard()
}

func resolveDraftVerify(override *bool) bool {
	if override != nil {
		return *override
	}
	return false
}
