package webgui

// 1.0.38: full-app browser checks for the user's orchestration report
// ("it says working but isn't working; the coordinator stopped pursuing the
// goal"). The real GUI, handlers, agent loops and worker registry run against
// an in-process scripted provider in an isolated temp data dir.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
)

// fix1038Provider:
//   - user "SPAWN x"   -> task{async:true}; then "Spawned x."
//   - automatic wake   -> "Wake acknowledged." (no tool: no progress)
//   - worker           -> streams a large create_file call (ToolArgs
//     progress), waits for one token on step, then reports.
type fix1038Provider struct {
	step        chan struct{}
	parentCalls atomic.Int32
	wakes       atomic.Int32
	wakeHints   atomic.Int32
}

func (*fix1038Provider) Name() string { return "fix1038-fixture" }

func (p *fix1038Provider) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	parent := false
	for _, def := range defs {
		parent = parent || def.Name == "task"
	}
	if !parent {
		out := make(chan llm.Delta, 4)
		go func() {
			defer close(out)
			out <- llm.Delta{OutputStarted: true}
			out <- llm.Delta{ToolArgs: &llm.ToolArgsProgress{Name: "create_file", Bytes: 7351}}
			select {
			case <-p.step:
			case <-ctx.Done():
				return
			}
			out <- llm.Delta{Content: "WORKER-REPORT: file written", FinishReason: "stop"}
		}()
		return out, nil
	}
	p.parentCalls.Add(1)
	last := msgs[len(msgs)-1]
	user := ""
	for i := len(msgs) - 1; i >= 0; i-- {
		if llm.IsConversationUserTurn(msgs[i]) {
			user = msgs[i].Content
			break
		}
	}
	freshReport := false
	for i := len(msgs) - 1; i >= 0 && msgs[i].Role != llm.RoleAssistant && msgs[i].Role != llm.RoleTool; i-- {
		freshReport = freshReport || msgs[i].Name == llm.WorkerNotificationName
	}
	if last.Role == llm.RoleTool {
		return workerSteeringWireDeltas(llm.Delta{Content: "Spawned " + clipFix1037(user) + ".", FinishReason: "stop"}), nil
	}
	if freshReport {
		p.wakes.Add(1)
		for _, m := range msgs {
			if strings.Contains(m.Content, "keep two agents busy") && strings.Contains(m.Content, "automatic wake-up") {
				p.wakeHints.Add(1)
				break
			}
		}
		return workerSteeringWireDeltas(llm.Delta{Content: "Wake acknowledged.", FinishReason: "stop"}), nil
	}
	if strings.HasPrefix(user, "SPAWN") {
		return workerSteeringWireCall(fmt.Sprintf("spawn-%d", p.parentCalls.Load()), "task",
			map[string]any{"agent": "explore", "prompt": "WORKER-1038 write the file (" + clipFix1037(user) + ")", "async": true})
	}
	return workerSteeringWireDeltas(llm.Delta{Content: "REPLY to " + clipFix1037(user), FinishReason: "stop"}), nil
}

func fix1038StartApp(t *testing.T, p *fix1038Provider, maxAuto int) *audit130App {
	t.Helper()
	app := audit130StartApp(t, 1280, 900)
	cfg := fmt.Sprintf("orchestrator = true\npreflight_repo = false\nbackground_wake_debounce_ms = 30\nbackground_wake_max_auto = %d\n", maxAuto)
	if err := os.WriteFile(filepath.Join(app.srv.eng.DataDir(), "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	app.srv.eng.mu.Lock()
	app.srv.eng.prov = p
	app.srv.eng.mu.Unlock()
	app.setLanguage("pl")
	return app
}

const fix1038State = `(()=>{const b=document.querySelector('#background-agents');const h=document.querySelector('#wake-held');
const pl=document.querySelector('#worker-overview .worker-overview-live:not([hidden])');
return {badge:b.textContent,badgeHidden:b.hidden,held:!h.hidden,heldText:h.textContent,status:document.querySelector('#run-status').textContent,
live:[...document.querySelectorAll('#worker-overview .worker-overview-live:not([hidden])')].map(n=>n.textContent),
summary:(document.querySelector('#worker-overview-summary')||{}).textContent||'',
acks:(document.querySelector('#stream').textContent.match(/Wake acknowledged/g)||[]).length};})()`

type fix1038View struct {
	Badge       string   `json:"badge"`
	BadgeHidden bool     `json:"badgeHidden"`
	Held        bool     `json:"held"`
	HeldText    string   `json:"heldText"`
	Status      string   `json:"status"`
	Live        []string `json:"live"`
	Summary     string   `json:"summary"`
	Acks        int      `json:"acks"`
}

// The user's case end to end: workers streaming a large file show "model
// writing create_file (7 KB)" (not "Creating file running"), the composer
// says "Working in background" while the coordinator is idle, automatic
// wake-ups without progress stop at the cap with a visible banner, and one
// click on Resume continues without typing a message.
func TestFix1038WorkerPhaseBadgeHeldBannerAndResume(t *testing.T) {
	p := &fix1038Provider{step: make(chan struct{}, 16)}
	app := fix1038StartApp(t, p, 1)
	app.send("SPAWN first: keep two agents busy until the file set is done")
	app.waitJS("first spawn done", `!streaming&&document.querySelector('#stream').textContent.includes('Spawned SPAWN first')`, 15*time.Second)
	app.send("SPAWN second")
	app.waitJS("second spawn done", `!streaming&&document.querySelector('#stream').textContent.includes('Spawned SPAWN second')`, 15*time.Second)

	// (a) The phase says the model is still writing the call, with its size.
	app.waitJS("args phase", `[...document.querySelectorAll('#worker-overview .worker-overview-live:not([hidden])')].some(n=>n.textContent.includes('model pisze: Tworzenie pliku (7.2 KB)'))`, 15*time.Second)
	// (c) Coordinator idle, two workers running: the badge says so.
	app.waitJS("badge", `document.querySelector('#background-agents').textContent.includes('Pracuje w tle (2)')`, 10*time.Second)
	var v fix1038View
	app.eval(fix1038State, &v)
	t.Logf("two workers writing: %+v", v)
	if v.BadgeHidden || v.Held || strings.Contains(strings.Join(v.Live, " "), "Tworzenie pliku działa") {
		t.Fatalf("wrong state while workers write: %+v", v)
	}
	app.cleanShot("1038_workers_writing", false)

	// First report: one automatic turn without progress (cap = 1).
	p.step <- struct{}{}
	app.waitJS("first wake turn", `(document.querySelector('#stream').textContent.match(/Wake acknowledged/g)||[]).length>=1&&!streaming`, 20*time.Second)
	// Second report: held at the cap, a persistent banner instead of a toast.
	p.step <- struct{}{}
	app.waitJS("held banner", `!document.querySelector('#wake-held').hidden`, 20*time.Second)
	app.eval(fix1038State, &v)
	t.Logf("held: %+v", v)
	if !strings.Contains(v.HeldText, "Automatyczne wybudzanie wstrzymane") || !strings.Contains(v.HeldText, "czeka 1 raport") || !strings.Contains(v.HeldText, "Wznów") || v.Acks != 1 {
		t.Fatalf("held banner wrong: %+v", v)
	}
	app.cleanShot("1038_wake_held_banner", false)

	// A refreshed view still shows the banner (sync snapshot).
	var sid string
	app.eval(`activeSessionID`, &sid)
	app.reload()
	app.setLanguage("pl")
	app.eval(`(async()=>{await loadSessions();await resumeSession(`+jsString(sid)+`,sessionByID[`+jsString(sid)+`]||null);return true;})()`, nil)
	app.waitJS("banner after refresh", `!document.querySelector('#wake-held').hidden`, 15*time.Second)

	// One click resumes: the held report gets its automatic turn.
	app.eval(`document.querySelector('#wake-held .wake-held-resume').click();true`, nil)
	app.waitJS("resumed wake turn", `(document.querySelector('#stream').textContent.match(/Wake acknowledged/g)||[]).length>=2&&!streaming`, 20*time.Second)
	app.waitJS("banner gone", `document.querySelector('#wake-held').hidden`, 10*time.Second)
	app.eval(fix1038State, &v)
	t.Logf("resumed: %+v", v)
	if p.wakes.Load() != 2 {
		t.Fatalf("automatic turns = %d, want 2", p.wakes.Load())
	}
	if p.wakeHints.Load() != 2 {
		t.Fatalf("wake turns carrying the user's goal = %d, want 2", p.wakeHints.Load())
	}
	if !v.BadgeHidden {
		t.Fatalf("badge still shown with no running worker: %+v", v)
	}
	app.cleanShot("1038_resumed", false)
}
