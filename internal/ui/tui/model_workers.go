package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"supercli/internal/agent"
)

type workerView struct {
	id, agent, status, activity string
}

func workerDisplayName(id string) string {
	return strings.Replace(id, "worker-", "Worker ", 1)
}

// Updates arrive on Bubble Tea's event loop. Rendering never queries a worker
// or a provider, and the panel keeps only compact presentation state.
func (m *Model) updateWorkerView(e agent.WorkerProgressEvent) {
	if e.TaskID == "" {
		return
	}
	var w *workerView
	for i := range m.workerViews {
		if m.workerViews[i].id == e.TaskID {
			w = &m.workerViews[i]
			break
		}
	}
	if w == nil {
		// Match the worker registry's retention without growing forever.
		if len(m.workerViews) >= 26 {
			for i, old := range m.workerViews {
				if old.status != "running" {
					m.workerViews = append(m.workerViews[:i], m.workerViews[i+1:]...)
					break
				}
			}
		}
		m.workerViews = append(m.workerViews, workerView{id: e.TaskID})
		w = &m.workerViews[len(m.workerViews)-1]
	}
	if e.Agent != "" {
		w.agent = e.Agent
	}
	switch e.Kind {
	case "started":
		w.status = "running"
		w.activity = compactWorkerText(e.Prompt, 140)
	case "tool_call":
		w.status = "running"
		w.activity = e.Tool
	case "tool_result":
		w.activity = m.tr("preparing response", "przygotowuje odpowiedź")
		if e.Err != "" {
			w.activity = compactWorkerText(e.Err, 140)
		}
	case "finished":
		w.status = e.Status
		w.activity = ""
	}
	m.resizeViewport()
}

func (m Model) workerPanelLines() []string {
	if len(m.workerViews) == 0 || (m.height > 0 && m.height < 16) {
		return nil
	}
	limit := 7
	if m.height > 0 && m.height/4 < limit {
		limit = m.height / 4
	}
	workers := append([]workerView(nil), m.workerViews...)
	sort.SliceStable(workers, func(i, j int) bool {
		a, b := workers[i], workers[j]
		if (a.status == "running") != (b.status == "running") {
			return a.status == "running"
		}
		an, _ := strconv.Atoi(strings.TrimPrefix(a.id, "worker-"))
		bn, _ := strconv.Atoi(strings.TrimPrefix(b.id, "worker-"))
		return an < bn
	})
	active := 0
	for _, w := range workers {
		if w.status == "running" {
			active++
		}
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	lines := []string{m.palette.InputHint.Render(ansi.Truncate(
		fmt.Sprintf("%s · %s: %d / %d", m.tr("Workers", "Workerzy"),
			m.tr("active", "pracuje"), active, len(workers)), width, "…"))}
	slots := limit - 1
	if len(workers) > slots {
		slots--
	}
	for i, w := range workers {
		if i >= slots {
			break
		}
		state, icon := m.tr("working", "pracuje"), "●"
		style := m.palette.HeaderMode
		switch w.status {
		case "done":
			state, icon, style = m.tr("done", "gotowe"), "✓", m.palette.Success
		case "failed":
			state, icon, style = m.tr("failed", "błąd"), "×", m.palette.Error
		case "stopped":
			state, icon, style = m.tr("stopped", "zatrzymano"), "–", m.palette.InputHint
		}
		line := fmt.Sprintf("%s %s · %s · %s", icon, workerDisplayName(w.id), w.agent, state)
		if w.activity != "" {
			line += " · " + w.activity
		}
		lines = append(lines, style.Render(ansi.Truncate(line, width, "…")))
	}
	if len(workers) > slots {
		lines = append(lines, m.palette.InputHint.Render(ansi.Truncate(
			fmt.Sprintf("+%d · /workers", len(workers)-slots), width, "…")))
	}
	return lines
}
