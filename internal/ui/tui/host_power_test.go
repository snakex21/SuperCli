package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"supercli/internal/system/hostpower"
	"supercli/internal/tools"
)

func hostPowerUIFixture(t *testing.T) *hostpower.Controller {
	t.Helper()
	c := hostpower.New(hostpower.Options{Execute: func(context.Context) error { t.Error("fixture must never execute"); return nil }, AfterFunc: func(time.Duration, func()) func() { return func() {} }})
	t.Cleanup(c.Close)
	return c
}

func armHostPowerUI(t *testing.T, c *hostpower.Controller, job string) {
	t.Helper()
	c.Begin(job)
	if err := c.Arm(job, "goal", strings.Repeat("C:/long/path/", 30), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestHostPowerInitializationAndExistingSpinnerNoticesRearmAfterTerminal(t *testing.T) {
	c := hostPowerUIFixture(t)
	armHostPowerUI(t, c, "first")
	m := New(Options{Home: t.TempDir(), HostPower: c, NoColor: true})
	if m.hostPower != c || m.hostPowerState.Status != "armed" {
		t.Fatalf("initial=%+v", m.hostPowerState)
	}
	c.Cancel()
	next, _ := m.Update(m.spinner.Tick())
	m = next.(Model)
	if m.hostPowerState.Status != "cancelled" {
		t.Fatalf("cancel=%+v", m.hostPowerState)
	}
	c.Finish("first", false)
	armHostPowerUI(t, c, "second")
	next, cmd := m.Update(m.spinner.Tick())
	m = next.(Model)
	if m.hostPowerState.Status != "armed" || cmd == nil {
		t.Fatalf("rearmed=%+v cmd=%v", m.hostPowerState, cmd != nil)
	}
	c.Finish("second", true)
	next, _ = m.Update(m.spinner.Tick())
	m = next.(Model)
	if m.hostPowerState.Status != "countdown" || m.hostPowerState.Deadline <= time.Now().UnixMilli() {
		t.Fatalf("deadline=%+v", m.hostPowerState)
	}
}

func TestHostPowerNormalEscapeCancelsPowerWithoutAgent(t *testing.T) {
	for _, countdown := range []bool{false, true} {
		t.Run(fmt.Sprint(countdown), func(t *testing.T) {
			c := hostPowerUIFixture(t)
			armHostPowerUI(t, c, "job")
			if countdown {
				c.Finish("job", true)
			}
			m := New(Options{Home: t.TempDir(), HostPower: c})
			m.busy = true
			cancelled := false
			m.cancel.Arm(cancelRun, func() { cancelled = true })
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
			m = next.(Model)
			if c.Snapshot().Status != "cancelled" || m.hostPowerState.Status != "cancelled" {
				t.Fatal("power not cancelled")
			}
			if cancelled || !m.busy || m.cancelling {
				t.Fatal("Escape cancelled agent as well as host")
			}
		})
	}
}

func TestHostPowerEscapeRespectsInputMenusAndConfirmation(t *testing.T) {
	for _, kind := range []string{"input", "menu", "confirmation", "autocomplete"} {
		t.Run(kind, func(t *testing.T) {
			c := hostPowerUIFixture(t)
			armHostPowerUI(t, c, "job")
			m := New(Options{Home: t.TempDir(), HostPower: c})
			var replies chan tools.AskAnswer
			switch kind {
			case "input":
				m.input.SetValue("unsent draft")
			case "menu":
				m.mode = modeMenu
			case "confirmation":
				req := sampleRequest()
				req.Confirmation = true
				replies = req.Respond
				next, _ := m.beginAsk(req)
				m = next.(Model)
			case "autocomplete":
				m.autocomp = autocomplete{kind: autocompSlash}
			}
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
			m = next.(Model)
			if c.Snapshot().Status != "armed" {
				t.Fatal("overlay/draft Escape cancelled host")
			}
			if kind == "confirmation" {
				select {
				case ans := <-replies:
					if !ans.Cancelled {
						t.Fatal("confirmation not cancelled")
					}
				default:
					t.Fatal("confirmation Escape lost")
				}
			}
		})
	}
}

func TestHostPowerFooterStatusesDeadlineAndTerminalDismissal(t *testing.T) {
	c := hostPowerUIFixture(t)
	m := New(Options{Home: t.TempDir(), HostPower: c, NoColor: true})
	m.width, m.height = 60, 24
	for _, status := range []string{"armed", "countdown", "blocked", "failed", "cancelled", "executing", "executed"} {
		m.hostPowerState = hostpower.Snapshot{Status: status, Goal: strings.Repeat("long/path", 100), Deadline: time.Now().Add(17 * time.Second).UnixMilli()}
		view := ansi.Strip(m.renderHostPower())
		if view == "" || !strings.Contains(view, m.tr("hostPower.title")) {
			t.Fatalf("%s: %q", status, view)
		}
		if status == "countdown" && (!strings.Contains(view, "17s") || !strings.Contains(view, m.tr("hostPower.escape"))) {
			t.Fatalf("not server deadline: %q", view)
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > m.width {
				t.Fatalf("overflow %q", line)
			}
		}
	}
	armHostPowerUI(t, c, "dismiss")
	c.Finish("dismiss", false)
	m.refreshHostPower()
	if m.hostPowerState.Status != "blocked" {
		t.Fatalf("state=%+v", m.hostPowerState)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.renderHostPower() != "" {
		t.Fatal("terminal banner not dismissed")
	}
	armHostPowerUI(t, c, "new")
	m.refreshHostPower()
	if m.renderHostPower() == "" {
		t.Fatal("new plan stayed dismissed")
	}
}

func TestHostPowerFooterFitsPhoneAndDesktopGeometry(t *testing.T) {
	for _, size := range [][2]int{{40, 14}, {60, 18}, {100, 30}} {
		c := hostPowerUIFixture(t)
		armHostPowerUI(t, c, "geometry")
		c.Finish("geometry", true)
		m := New(Options{Home: t.TempDir(), HostPower: c, NoColor: true})
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = next.(Model)
		view := ansi.Strip(m.View())
		if len(strings.Split(view, "\n")) > size[1] {
			t.Fatalf("%v: overflow rows=%d\n%s", size, len(strings.Split(view, "\n")), view)
		}
		if !strings.Contains(view, m.tr("hostPower.escape")) {
			t.Fatalf("%v: lost prominent cancel\n%s", size, view)
		}
	}
}

func TestHostPowerTrustedQuestionLocalizationAndLiteralGoal(t *testing.T) {
	m := New(Options{Home: t.TempDir()})
	goal := "<img> C:/long/path {seconds} {goal}"
	req := tools.AskRequest{Question: "fallback", QuestionKey: "hostPower.confirm", QuestionArgs: map[string]string{"goal": goal}, Confirmation: true}
	text := m.trustedQuestion(req)
	if text == "fallback" || !strings.Contains(text, goal) {
		t.Fatalf("localized=%q", text)
	}
	req.Confirmation = false
	if m.trustedQuestion(req) != "fallback" {
		t.Fatal("untrusted ask localized")
	}
	req.Confirmation = true
	req.QuestionKey = "unrecognized"
	if m.trustedQuestion(req) != "fallback" {
		t.Fatal("unknown metadata lost fallback")
	}
}
