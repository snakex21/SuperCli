package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"supercli/internal/system/config"
	"supercli/internal/system/uilang"
	"supercli/internal/system/updater"
	"supercli/internal/ui/tui"
)

func updateLanguage(dataDir, home string) string {
	cfg, err := config.ResolveConfig(dataDir, home, "")
	if err == nil {
		return uilang.Resolve(cfg.Language)
	}
	return uilang.Resolve("")
}
func formatUpdateState(state updater.State, language string) string {
	key := "update.current"
	switch state.Status {
	case "available":
		key = "update.available"
	case "downloaded":
		key = "update.downloaded"
	case "installed":
		key = "update.installed"
	case "unsupported":
		key = "update.unsupported"
	}
	lines := []string{uilang.Text(language, key), uilang.Text(language, "update.current_version") + ": " + state.CurrentVersion}
	if state.LatestVersion != "" {
		lines = append(lines, uilang.Text(language, "update.latest_version")+": "+state.LatestVersion)
	}
	if state.Asset != "" {
		lines = append(lines, state.Asset)
	}
	if state.URL != "" {
		lines = append(lines, state.URL)
	}
	if state.RestartRequired {
		lines = append(lines, uilang.Text(language, "update.restart_required"))
	}
	return strings.Join(lines, "\n")
}

func updateCommand(dataDir, home string) tui.SlashHandler {
	return func(ctx context.Context, raw string) (string, error) {
		language := updateLanguage(dataDir, home)
		action := strings.ToLower(strings.TrimSpace(raw))
		if action == "" {
			action = "check"
		}
		if action != "check" && action != "download" && action != "install" {
			return uilang.Text(language, "update.usage"), nil
		}
		manager, err := updater.New()
		if err != nil {
			return "", err
		}
		var state updater.State
		switch action {
		case "check":
			state, err = manager.Check(ctx)
		case "download":
			state, err = manager.Download(ctx)
		case "install":
			state, err = manager.Install(ctx)
		}
		if err != nil {
			return "", fmt.Errorf("%s: %w", uilang.Text(language, "update.failed"), err)
		}
		return formatUpdateState(state, language), nil
	}
}

type cliUpdater interface {
	Check(context.Context) (updater.State, error)
	Download(context.Context) (updater.State, error)
	Install(context.Context) (updater.State, error)
}

func performCLIUpdate(ctx context.Context, manager cliUpdater, action string) (updater.State, error) {
	if action == "check" {
		return manager.Check(ctx)
	}
	state, err := manager.Download(ctx)
	if err != nil {
		return state, err
	}
	// A current installation and an already installed update are successful no-ops.
	if state.Status != "downloaded" {
		return state, nil
	}
	return manager.Install(ctx)
}

func runUpdateCLI(action, dataDir, home string) error {
	manager, err := updater.New()
	if err != nil {
		return err
	}
	language := updateLanguage(dataDir, home)
	state, err := performCLIUpdate(context.Background(), manager, action)
	if err != nil {
		return fmt.Errorf("%s: %w", uilang.Text(language, "update.failed"), err)
	}
	fmt.Fprintln(os.Stdout, formatUpdateState(state, language))
	return nil
}
