package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/buildinfo"
	"supercli/internal/llm"
	"supercli/internal/llm/shuffler"
	"supercli/internal/system/config"
	"supercli/internal/system/doctor"
	"supercli/internal/ui/export"
)

// slashBuiltins are TUI-level command handlers that run before the
// generic m.commands map (agent-wired handlers). Order does not
// matter: lookup is by exact command name.
//
// Handlers that open menus or need Args=="" gating stay inline in
// dispatchSlashCommand — they are one-liners. Heavier text-path
// handlers live here as named methods so model_slash.go stays a
// short dispatcher.
func (m Model) slashBuiltin(name string) (func(Model, SlashCommand) (tea.Model, tea.Cmd), bool) {
	switch name {
	case "context-limit":
		return Model.handleSlashContextLimit, true
	case "plan":
		return Model.handleSlashPlan, true
	case "shuffle":
		return Model.handleSlashShuffle, true
	case "export":
		return Model.handleSlashExport, true
	case "diff":
		return Model.handleSlashDiff, true
	case "model":
		return Model.handleSlashModel, true
	case "undo":
		return Model.handleSlashUndo, true
	case "redo":
		return Model.handleSlashRedo, true
	case "providers":
		return Model.handleSlashProviders, true
	case "doctor":
		return Model.handleSlashDoctor, true
	case "cost":
		return Model.handleSlashCost, true
	default:
		return nil, false
	}
}

func (m Model) handleSlashContextLimit(cmd SlashCommand) (tea.Model, tea.Cmd) {
	provider := m.activeProviderName()
	model := ""
	if m.modelSwapper != nil {
		model = m.modelSwapper.CurrentModel()
	}
	if model == "" && m.llm != nil {
		model = m.llm.Name()
	}
	if m.modelContexts == nil || provider == "" || model == "" {
		msg := m.tr("tui.cmd_slash_handlers.063f295193")
		return m, func() tea.Msg { return slashResultMsg{Body: m.marker.Diff(msg)} }
	}
	if strings.TrimSpace(cmd.Args) == "" {
		value := "auto"
		if tokens, ok := m.modelContexts.Get(provider, model); ok {
			value = fmt.Sprintf("%d", tokens)
		}
		msg := fmt.Sprintf("%s / %s: %s", provider, model, value)
		return m, func() tea.Msg { return slashResultMsg{Body: m.marker.Diff(msg)} }
	}
	tokens, automatic, err := config.ParseContextBudget(cmd.Args)
	if err != nil {
		return m, func() tea.Msg { return slashResultMsg{Body: m.marker.Error(err)} }
	}
	if automatic {
		_, err = m.modelContexts.Remove(provider, model)
	} else {
		err = m.modelContexts.Set(provider, model, tokens)
	}
	if err != nil {
		return m, func() tea.Msg { return slashResultMsg{Body: m.marker.Error(err)} }
	}
	value := "auto"
	if !automatic {
		value = fmt.Sprintf(m.tr("tui.slash.f01d4b5d68"), tokens, tokens*80/100)
	}
	msg := fmt.Sprintf("%s / %s: %s", provider, model, value)
	return m, func() tea.Msg { return slashResultMsg{Body: m.marker.Diff(msg)} }
}

func (m Model) handleSlashPlan(_ SlashCommand) (tea.Model, tea.Cmd) {
	m.planMode = !m.planMode
	on := m.planMode
	return m, func() tea.Msg {
		return slashResultMsg{Body: m.marker.PlanMode(on)}
	}
}

func (m Model) handleSlashShuffle(cmd SlashCommand) (tea.Model, tea.Cmd) {
	dm := m.marker
	args := cmd.Args
	return m, func() tea.Msg {
		parts := strings.Fields(args)
		if len(parts) == 0 {
			return slashResultMsg{Body: dm.Diff(shuffler.Global.Status() + "\n\n/shuffle auto|on|off|add|load|list|status|check|now|interval")}
		}
		switch parts[0] {
		case "auto":
			msg := shuffler.Global.AutoConfigure(context.Background(), 5*time.Minute)
			return slashResultMsg{Body: dm.Diff(msg)}
		case "on":
			shuffler.Global.Enable()
			return slashResultMsg{Body: dm.Diff(shuffler.Global.Status())}
		case "off":
			shuffler.Global.Disable()
			return slashResultMsg{Body: dm.Diff(shuffler.Global.Status())}
		case "add":
			if len(parts) < 2 {
				return slashResultMsg{Body: dm.Diff(m.tr("tui.slash.8a5f671483"))}
			}
			if err := shuffler.Global.AddProxy(parts[1]); err != nil {
				return slashResultMsg{Err: err}
			}
			return slashResultMsg{Body: dm.Diff(fmt.Sprintf(m.tr("tui.slash.55c30b2413"), parts[1], shuffler.Global.Status()))}
		case "load":
			if len(parts) < 2 {
				return slashResultMsg{Body: dm.Diff(m.tr("tui.slash.0b45a51e8a"))}
			}
			if err := shuffler.Global.LoadFromURL(context.Background(), parts[1]); err != nil {
				return slashResultMsg{Err: err}
			}
			return slashResultMsg{Body: dm.Diff(fmt.Sprintf(m.tr("tui.slash.81dbcab7e2"), parts[1], shuffler.Global.Status()))}
		case "list":
			proxies := shuffler.Global.List()
			if len(proxies) == 0 {
				return slashResultMsg{Body: dm.Diff(m.tr("tui.slash.d97d31da9b"))}
			}
			var b strings.Builder
			b.WriteString(m.tr("tui.slash.2b94e29bf3"))
			for _, p := range proxies {
				marker := " "
				if shuffler.Global.IsEnabled() {
					if cur := shuffler.Global.GetCurrentProxy(); cur == p {
						marker = "*"
					}
				}
				b.WriteString(fmt.Sprintf("  %s %s\n", marker, p))
			}
			b.WriteString("\n")
			b.WriteString(shuffler.Global.Status())
			return slashResultMsg{Body: dm.Diff(b.String())}
		case "status":
			return slashResultMsg{Body: dm.Diff(shuffler.Global.Status())}
		case "check":
			statuses := shuffler.Global.CheckProxies(context.Background(), "")
			var b strings.Builder
			b.WriteString(m.tr("tui.slash.e3b628476d"))
			for _, st := range statuses {
				icon := "OK"
				if !st.OK {
					icon = "FAIL"
				}
				b.WriteString(fmt.Sprintf("  [%s] %s", icon, st.Proxy))
				if st.Err != "" {
					b.WriteString(fmt.Sprintf(" — %s", st.Err))
				}
				b.WriteString("\n")
			}
			return slashResultMsg{Body: dm.Diff(b.String())}
		case "now":
			newProxy := shuffler.Global.Rotate()
			if newProxy == "" {
				return slashResultMsg{Body: dm.Diff(m.tr("tui.slash.5a811e5c4f"))}
			}
			return slashResultMsg{Body: dm.Diff(fmt.Sprintf(m.tr("tui.slash.c578e79a15"), newProxy, shuffler.Global.Status()))}
		case "interval":
			if len(parts) < 2 {
				return slashResultMsg{Body: dm.Diff(m.tr("tui.slash.97d08a8086"))}
			}
			var secs int
			fmt.Sscanf(parts[1], "%d", &secs)
			shuffler.Global.SetInterval(time.Duration(secs) * time.Second)
			return slashResultMsg{Body: dm.Diff(fmt.Sprintf(m.tr("tui.slash.525d2333ba"), secs, shuffler.Global.Status()))}
		default:
			return slashResultMsg{Body: dm.Diff(m.tr("tui.slash.414c5b4cf0") + parts[0] + "\n\n/shuffle auto|on|off|add|load|list|status|check|now|interval")}
		}
	}
}

func (m Model) handleSlashExport(cmd SlashCommand) (tea.Model, tea.Cmd) {
	store := m.sessionStore
	dm := m.marker
	home := m.home
	if m.dataDir != "" {
		home = m.dataDir
	}
	sessionID := m.sessionID
	args := cmd.Args
	return m, func() tea.Msg {
		if store == nil {
			return slashResultMsg{Body: dm.Diff(m.tr("tui.slash.11acbac2d2"))}
		}
		if sessionID == "" {
			return slashResultMsg{Body: dm.Diff(m.tr("tui.cmd_slash_handlers.3e78b47f2f"))}
		}
		sess, err := store.Get(sessionID)
		if err != nil {
			return slashResultMsg{Err: err}
		}
		msgs, err := store.ReadMessages(context.Background(), sess.ID)
		if err != nil {
			return slashResultMsg{Err: fmt.Errorf(m.tr("tui.slash.88d8c382f3"), err)}
		}
		opts := export.Options{
			ID:        sess.ID,
			Title:     sess.Title,
			Model:     sess.Model,
			Cwd:       sess.Cwd,
			CreatedAt: sess.CreatedAt,
			UpdatedAt: sess.UpdatedAt,
			TokensIn:  sess.TokenIn,
			TokensOut: sess.TokenOut,
			Messages:  msgs,
		}
		content := export.RenderMarkdown(opts)
		if a := strings.TrimSpace(args); a == "clip" || a == "clipboard" {
			if err := clipboard.WriteAll(content); err != nil {
				return slashResultMsg{Err: fmt.Errorf(m.tr("tui.slash.8e0d97abc6"), err)}
			}
			return slashResultMsg{Body: dm.ModelInfo(fmt.Sprintf(m.tr("tui.slash.49aed96c7c"), len(msgs)))}
		}
		filename := export.DefaultFilename(opts)
		if args != "" {
			filename = strings.TrimSpace(args)
		}
		path := home + "/" + filename
		if err := writeExportFile(path, content); err != nil {
			return slashResultMsg{Err: fmt.Errorf(m.tr("tui.slash.127b72ef9e"), err)}
		}
		return slashResultMsg{Body: dm.ModelInfo(fmt.Sprintf(m.tr("tui.slash.797e916342"), len(msgs), path))}
	}
}

func (m Model) handleSlashDiff(_ SlashCommand) (tea.Model, tea.Cmd) {
	tracker := m.tracker
	dm := m.marker
	return m, func() tea.Msg {
		if tracker == nil || tracker.Count() == 0 {
			return slashResultMsg{Body: dm.Diff(m.tr("tui.slash.807564bac8"))}
		}
		return slashResultMsg{Body: dm.Diff(tracker.DiffOutput())}
	}
}

func (m Model) handleSlashModel(cmd SlashCommand) (tea.Model, tea.Cmd) {
	if cmd.Args == "" {
		return m.openModelsMenu()
	}
	if m.modelSwapper == nil {
		return m, func() tea.Msg {
			return slashResultMsg{Body: m.marker.Diff(m.tr("tui.slash.76296fd689"))}
		}
	}
	if m.modelLister == nil {
		return m, func() tea.Msg {
			return slashResultMsg{Body: m.marker.Diff(m.tr("tui.slash.43d66da64e"))}
		}
	}
	previousMenu := m.menu
	m.menu.kind = menuModels
	models := m.filteredModelRows()
	m.menu = previousMenu
	target := strings.TrimSpace(cmd.Args)
	var found *llm.ModelInfo
	for i := range models {
		if m.providerMgr != nil {
			if !m.providerMgr.ModelVisible(models[i].Provider, models[i].ID) || m.providerMgr.IsHiddenFor(models[i].Provider, models[i].ID) {
				continue
			}
		}
		if models[i].ID == target || strings.Contains(models[i].ID, target) {
			found = &models[i]
			break
		}
	}
	if found == nil {
		errMsg := fmt.Sprintf(m.tr("tui.slash.d9a6438d82"), target)
		return m, func() tea.Msg {
			return slashResultMsg{Body: m.marker.Diff(errMsg)}
		}
	}
	modelID := found.ID
	provider := found.Provider
	return m, func() tea.Msg {
		return modelSwapRequestMsg{ModelID: modelID, Provider: provider}
	}
}

func (m Model) handleSlashUndo(cmd SlashCommand) (tea.Model, tea.Cmd) {
	if m.checkpointUndo != nil {
		undo := m.checkpointUndo
		return m, func() tea.Msg {
			body, err := undo(context.Background(), false)
			return slashResultMsg{Body: body, Err: err}
		}
	}
	trk := m.tracker
	dm := m.marker
	args := cmd.Args
	return m, func() tea.Msg {
		if trk == nil {
			return slashResultMsg{Body: dm.Diff(m.tr("tui.slash.5513721fb3"))}
		}
		n := 1
		if args != "" {
			fmt.Sscanf(args, "%d", &n)
		}
		results, err := trk.Undo(n)
		if err != nil {
			return slashResultMsg{Err: err}
		}
		if len(results) == 0 {
			return slashResultMsg{Body: dm.Diff(m.tr("tui.slash.7d1429d9b1"))}
		}
		var b strings.Builder
		fmt.Fprintf(&b, m.tr("tui.slash.837e6b2747"), len(results))
		for _, r := range results {
			fmt.Fprintf(&b, "  %s (%s)\n", r.Path, r.Op)
		}
		return slashResultMsg{Body: dm.Diff(b.String())}
	}
}

func (m Model) handleSlashRedo(_ SlashCommand) (tea.Model, tea.Cmd) {
	if m.checkpointUndo == nil {
		return m, func() tea.Msg { return slashResultMsg{Err: fmt.Errorf("%s", m.tr("tui.slash.2eaece8ebf"))} }
	}
	redo := m.checkpointUndo
	return m, func() tea.Msg {
		body, err := redo(context.Background(), true)
		return slashResultMsg{Body: body, Err: err}
	}
}

func (m Model) handleSlashProviders(cmd SlashCommand) (tea.Model, tea.Cmd) {
	// Bare /providers opens the interactive menu (handled by the
	// dispatcher before this method is called). With args, run text
	// subcommands.
	mgr := m.providerMgr
	dm := m.marker
	args := cmd.Args
	return m, func() tea.Msg {
		if mgr == nil {
			return slashResultMsg{Body: dm.Diff(m.tr("tui.slash.77ae580bb6"))}
		}
		mgr.Reload()
		parts := strings.Fields(args)
		if len(parts) == 0 {
			return slashResultMsg{Body: dm.Diff(renderProvidersList(mgr, nil, m.language))}
		}
		switch parts[0] {
		case "add":
			if len(parts) < 4 {
				return slashResultMsg{Body: dm.Diff("/providers add <name> <type> <base_url> [api_key]")}
			}
			apiKey := ""
			if len(parts) >= 5 {
				apiKey = parts[4]
			}
			err := mgr.Add(parts[1], parts[2], parts[3], apiKey, "")
			if err != nil {
				return slashResultMsg{Err: err}
			}
			return slashResultMsg{Body: dm.Diff(fmt.Sprintf(m.tr("tui.slash.0680f927c9"), parts[1]))}
		case "remove", "rm":
			if len(parts) < 2 {
				return slashResultMsg{Body: dm.Diff("/providers remove <name>")}
			}
			err := mgr.Remove(parts[1])
			if err != nil {
				return slashResultMsg{Err: err}
			}
			return slashResultMsg{Body: dm.Diff(fmt.Sprintf(m.tr("tui.slash.a2d43dde8d"), parts[1]))}
		case "price":
			if len(parts) < 4 {
				return slashResultMsg{Body: dm.Diff("/providers price <model_id> <input_cost_per_1M> <output_cost_per_1M>")}
			}
			var inputCost, outputCost float64
			fmt.Sscanf(parts[2], "%f", &inputCost)
			fmt.Sscanf(parts[3], "%f", &outputCost)
			err := mgr.SetPrice(parts[1], inputCost, outputCost)
			if err != nil {
				return slashResultMsg{Err: err}
			}
			return slashResultMsg{Body: dm.Diff(fmt.Sprintf(m.tr("tui.slash.6963013366"), parts[1], inputCost, outputCost))}
		case "toggle":
			ref := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(args), "toggle"))
			provider, modelID, ok := strings.Cut(ref, "::")
			if !ok || strings.TrimSpace(provider) == "" || strings.TrimSpace(modelID) == "" {
				return slashResultMsg{Body: dm.Diff(m.tr("tui.slash.1d6b6af383"))}
			}
			hidden := mgr.ToggleHiddenFor(strings.TrimSpace(provider), strings.TrimSpace(modelID))
			state := m.tr("tui.menu_models_render.37f4a09472")
			if hidden {
				state = m.tr("tui.menu_models_render.5ad9cfa728")
			}
			return slashResultMsg{Body: dm.Diff(fmt.Sprintf(m.tr("tui.slash.c9511a2999"), provider, modelID, state))}
		default:
			return slashResultMsg{Body: dm.Diff(renderProvidersList(mgr, nil, m.language))}
		}
	}
}

func (m Model) handleSlashDoctor(_ SlashCommand) (tea.Model, tea.Cmd) {
	env := doctor.Env{
		Version:     buildinfo.Version,
		Home:        m.home,
		DataDir:     m.dataDir,
		Provider:    m.llm,
		Registry:    m.toolRegistry,
		Sessions:    m.sessionStore,
		ProviderMgr: m.providerMgr,
		Caps:        m.caps,
	}
	m.appendLine(m.marker.Running())
	m.refreshTranscript()
	return m, func() tea.Msg {
		rep := doctor.Run(context.Background(), env)
		return doctorReportMsg{report: &rep}
	}
}

func (m Model) handleSlashCost(_ SlashCommand) (tea.Model, tea.Cmd) { return m.openUsageMenu() }
