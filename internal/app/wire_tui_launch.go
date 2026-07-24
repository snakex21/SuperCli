package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"supercli/internal/account/tier"
	"supercli/internal/agent"
	"supercli/internal/checkpoint"
	"supercli/internal/llm"
	"supercli/internal/llm/factory"
	"supercli/internal/llm/providers"
	"supercli/internal/storage/goal"
	"supercli/internal/storage/memory"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
	"supercli/internal/system/stats"
	"supercli/internal/tools"
	"supercli/internal/tools/fileops"
	"supercli/internal/tools/shellescape"
	"supercli/internal/ui/tui"
	"supercli/internal/webgui"
)

// tuiLaunchDeps is the closed-over state for building TUI Options.
type tuiLaunchDeps struct {
	home, dataDir, sessionID, version, uiLanguage string
	modelTier                                     tier.Tier
	loop                                          *agent.Loop
	provider                                      llm.Provider
	mergedCommands                                map[string]tui.SlashHandler
	statusFn                                      func() string
	checkpointCtrl                                *checkpoint.Controller
	memAutoSaver                                  *memory.AutoSaver
	memProg                                       *memProgress
	memIdle                                       *idleScheduler
	extCh                                         chan agent.Event
	sessStore                                     *session.Store
	draftStats                                    *stats.Memory
	provMgr                                       *providers.Manager
	initialContextProvider                        string
	modelContexts                                 *config.ModelContextStore
	caps                                          *llm.CapabilityRegistry
	goalSvc                                       *goal.Service
	registry                                      *tools.Registry
	provFactory                                   *factory.Factory
	cfg                                           config.Config
	redrawStatus                                  func()
}

// buildTUIOptions assembles tui.Options for the interactive CLI.
func buildTUIOptions(d tuiLaunchDeps) tui.Options {
	return tui.Options{
		Home:      d.home,
		DataDir:   d.dataDir,
		SessionID: d.sessionID,
		Version:   d.version,
		Tier:      string(d.modelTier),
		Language:  d.uiLanguage,
		Agent:     d.loop,
		LLM:       d.provider,
		Commands:  d.mergedCommands,
		StatusFn:  d.statusFn,
		// Incremental memory: after every finished agent turn,
		// deterministic user facts are saved immediately (no model
		// call) and the model-backed summary is scheduled for the
		// next idle window.
		OnRunEnd: func() {
			defer recoverAndLog(d.dataDir)()
			if d.checkpointCtrl != nil {
				cpCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				if _, err := d.checkpointCtrl.Complete(cpCtx); err != nil {
					log.Printf("checkpoint complete: %v", err)
				}
				cancel()
			}
			saveDeterministicMemoryFacts(d.memAutoSaver, d.loop, d.memProg)
			d.memIdle.Schedule()
		},
		// Foreground beats background: the moment the user submits
		// a new prompt, stop the idle timer and cancel any
		// in-flight background memory inference.
		OnRunStart: func() {
			d.memIdle.Activity()
			if d.checkpointCtrl != nil {
				d.checkpointCtrl.Start("")
			}
		},
		CheckpointUndo: func(ctx context.Context, redo bool) (string, error) {
			if d.checkpointCtrl == nil {
				return "", checkpoint.ErrUnavailable
			}
			var result checkpoint.Result
			var err error
			if redo {
				result, err = d.checkpointCtrl.Redo(ctx)
			} else {
				result, err = d.checkpointCtrl.Undo(ctx)
			}
			if err != nil {
				if len(result.Conflicts) > 0 {
					return "", fmt.Errorf("conflicts: %s", strings.Join(result.Conflicts, ", "))
				}
				return "", err
			}
			verb := "reverted"
			if redo {
				verb = "restored"
			}
			d.loop.InjectUserMessage(ctx, fmt.Sprintf("[checkpoint] User %s changes from turn %s (%d files). Current workspace state supersedes the earlier implementation.", verb, result.Record.ID, len(result.Files)))
			return fmt.Sprintf("%s %d file(s) from turn %s", verb, len(result.Files), result.Record.ID), nil
		},
		CheckpointPreview: func(redo bool) (tui.CheckpointPreview, error) {
			if d.checkpointCtrl == nil {
				return tui.CheckpointPreview{}, checkpoint.ErrUnavailable
			}
			record, err := d.checkpointCtrl.Preview(redo)
			if err != nil {
				return tui.CheckpointPreview{}, err
			}
			return tui.CheckpointPreview{
				ID: record.ID, Prompt: record.Prompt,
				Files: append([]string(nil), record.Files...), Redo: redo,
			}, nil
		},
		ExtCh:        d.extCh,
		ShellRunner:  shellescape.NewRunner(d.home),
		Tracker:      fileops.NewTracker(200),
		ModelSwapper: d.loop,
		ModelLister:  d.loop,
		ModelSwapFn: func(modelID, providerName string) (llm.Provider, error) {
			// Build a new provider with the target model.
			// Look up the provider's base URL and API key
			// from the config.toml providers list.
			swapCfg := d.cfg
			swapCfg.Model = modelID
			swapToml, _ := config.ResolveConfig(d.dataDir, d.home, "")
			if providerName != "" {
				for _, pc := range swapToml.Providers {
					if pc.Name == providerName {
						if pc.Disabled {
							return nil, fmt.Errorf("provider %q is disabled", providerName)
						}
						swapCfg.BaseURL = pc.BaseURL
						swapCfg.APIKey = pc.APIKey
						swapCfg.Provider = pc.Type
						break
					}
				}
			}
			// The factory keeps the model-call metering across /model swaps.
			np, err := d.provFactory.BuildChain(swapCfg, swapToml, llm.PurposeMain)
			if err == nil {
				// Just switched models — if the new provider is Codex,
				// refresh its usage snapshot in the background so the HUD
				// reflects the newly selected model's limits promptly.
				// redrawStatus forces the footer to re-render once the
				// fetch lands, so the `limit:` tile appears on its own
				// without the user pressing a key.
				kickCodexUsageRefresh(np, d.redrawStatus)
			}
			return np, err
		},
		SessionStore:       d.sessStore,
		StatsRecorder:      d.draftStats,
		ProviderMgr:        d.provMgr,
		ActiveProvider:     d.initialContextProvider,
		ModelContextStore:  d.modelContexts,
		CapabilityRegistry: d.caps,
		GoalService:        d.goalSvc,
		ToolRegistry:       d.registry,
		DataExport: func(_ context.Context, full bool) (string, error) {
			return webgui.ExportDataBackup(d.dataDir, full)
		},
		DataImport: func(_ context.Context, path string) (bool, error) {
			return webgui.StageDataImport(d.dataDir, path)
		},
	}
}
