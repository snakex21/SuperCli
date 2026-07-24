package app

import (
	"context"
	"fmt"
	"log"
	"os"

	"supercli/internal/system/config"
	"supercli/internal/ui/tui"
)

// maybeRunOnboarding walks the first-run wizard when nothing is configured
// (echo fallback, empty providers). Mutates tomlCfg and cfg in place when
// the user completes setup. ChatGPT auth runs on the plain console before
// the TUI starts.
func maybeRunOnboarding(echo bool, cfg *config.Config, tomlCfg *config.TomlConfig, dataDir, cwd, uiLanguage string) {
	// Wave 4: first-run onboarding. When nothing at all is
	// configured (no providers in config.toml, no env/flag
	// provider — the resolved provider fell back to echo
	// without the user asking for it), walk the user through
	// a minimal setup, persist config.toml, and continue into
	// chat with the chosen provider.
	if echo || !cfg.IsEcho() ||
		len(tomlCfg.Providers) > 0 || tomlCfg.Provider != "" || tomlCfg.DefaultProvider != "" {
		return
	}
	res := tui.RunOnboarding(uiLanguage)
	if res.Skipped {
		return
	}
	// "Sign in with ChatGPT" needs the OAuth browser flow,
	// which the wizard cannot run itself. Do it here, on
	// the plain console, before the TUI starts.
	if res.AuthMethod == tui.AuthChatGPT {
		initCodexAuth(dataDir, *tomlCfg)
		if _, err := codexAuthMgr.Login(context.Background(), os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "ChatGPT login failed: %v\nFalling back to setup-free start — run /login inside SuperCli to retry.\n", err)
		} else {
			res.BaseURL = codexAuthMgr.Options().BackendURL
		}
	}
	globalTomlPath, _ := config.FindTomlPaths(dataDir, cwd)
	saved := *tomlCfg
	saved.Providers = []config.ProviderConf{{
		Name:    res.Name,
		Type:    res.Type,
		BaseURL: res.BaseURL,
		APIKey:  res.APIKey,
		Model:   res.Model,
	}}
	saved.DefaultProvider = res.Name
	if res.Model != "" {
		saved.DefaultModel = res.Model
	}
	if err := config.SaveToml(globalTomlPath, saved); err != nil {
		log.Printf("onboarding: save config.toml: %v", err)
	}
	*tomlCfg = saved
	cfg.Provider = res.Type
	cfg.BaseURL = res.BaseURL
	cfg.APIKey = res.APIKey
	if res.Model != "" {
		cfg.Model = res.Model
	}
	if err := cfg.Normalize(); err != nil {
		log.Printf("onboarding: normalize config: %v", err)
	}
}
