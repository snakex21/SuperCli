package webgui

// 1.0.39 delegation panel, end to end in our own headless Chrome: the real
// GUI handler on a free loopback port, an isolated data directory, and a
// scripted fake model (no API call). Three workers end in different states
// (working, error, finished); the user then asks "i jak idzie?" while one is
// still working. The test checks the panel's facts and, when
// SUPERCLI_UI_SHOTS names a directory, saves screenshots there (the data
// directory then lives beside them, never in the user's supercli-data).

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/testbrowser"
)

const (
	panelSlowBrief = "Projekt: port Minecraft Java 1.12 do Zig (przeczytaj AGENTS.md). Zig: build-migration/tools/zig-x86_64-windows-0.15.2/zig.exe.\nCEL: dodać pająka, szkieleta i creepera w zwykłym świecie (zig-engine/src/native_world_mob_spawning_species.zig)."
	panelFailBrief = "Projekt: port Minecraft Java 1.12 do Zig.\nCEL: naprawić 3 testy zapisu nocy w zig-engine/src/native_night_world_save_tests.zig"
	panelOKBrief   = "Projekt: port Minecraft Java 1.12 do Zig.\nCEL: dodać ikony pancerza do HUD"
)

type panelProvider struct {
	release      chan struct{}
	parentCalls  atomic.Int32
	statusAsked  atomic.Int32
	statusRefuse atomic.Int32
}

func (*panelProvider) Name() string { return "panel-fixture" }

func (p *panelProvider) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	parent := false
	for _, def := range defs {
		parent = parent || def.Name == "task"
	}
	all := ""
	for _, m := range msgs {
		all += m.Content + "\n" + m.TextOnly().Content + "\n"
	}
	if !parent {
		firstUser := ""
		for _, m := range msgs {
			if m.Role == llm.RoleUser {
				firstUser = m.Content
				break
			}
		}
		switch {
		case strings.Contains(firstUser, "zapisu nocy"):
			return nil, errors.New("sse: stream error: stream ID 387; INTERNAL_ERROR; received from peer (HTTP 400)")
		case strings.Contains(firstUser, "ikony pancerza"):
			return workerSteeringWireDeltas(llm.Delta{Content: "Dodałem ikony pancerza do HUD. Testy HUD pancerza: 22/22.", FinishReason: "stop"}), nil
		default:
			out := make(chan llm.Delta, 4)
			go func() {
				defer close(out)
				out <- llm.Delta{OutputStarted: true}
				out <- llm.Delta{ToolArgs: &llm.ToolArgsProgress{Name: "patch_file", Bytes: 4210}}
				select {
				case <-p.release:
				case <-ctx.Done():
					return
				}
				out <- llm.Delta{Content: "Pająk, szkielet i creeper działają w zwykłym świecie.", FinishReason: "stop"}
			}()
			return out, nil
		}
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
	switch {
	case strings.Contains(user, "jak idzie"):
		if last.Role == llm.RoleTool {
			if strings.Contains(last.Content, "refused: the user only asked for a status update") {
				p.statusRefuse.Add(1)
			}
			return workerSteeringWireDeltas(llm.Delta{Content: panelStatusAnswer(all), FinishReason: "stop"}), nil
		}
		p.statusAsked.Add(1)
		// A careless model tries to resume work on a status question.
		return workerSteeringWireCall("status-resume", "send_message", map[string]any{"to": "worker-2", "message": "Wznowienie po błędzie: kontynuuj."})
	case last.Role == llm.RoleTool:
		return workerSteeringWireDeltas(llm.Delta{Content: "Uruchomiłem trzech agentów w tle. Napiszę, gdy skończą.", FinishReason: "stop"}), nil
	case freshReport:
		return workerSteeringWireDeltas(llm.Delta{Content: "Raport przyszedł; szczegóły są na kartach agentów.", FinishReason: "stop"}), nil
	case strings.Contains(user, "Rozpocznij"):
		out := make(chan llm.Delta, 4)
		for i, brief := range []string{panelSlowBrief, panelFailBrief, panelOKBrief} {
			out <- llm.Delta{ToolCall: &llm.ToolCall{ID: fmt.Sprintf("spawn-%d", i+1), Name: "task", Arguments: fmt.Sprintf(`{"agent":"code","prompt":%s}`, jsString(brief))}}
		}
		out <- llm.Delta{FinishReason: "tool_calls"}
		close(out)
		return out, nil
	}
	return workerSteeringWireDeltas(llm.Delta{Content: "OK.", FinishReason: "stop"}), nil
}

// panelStatusAnswer answers only from the worker states the status turn put
// into the request, the way a well-behaved model would.
func panelStatusAnswer(request string) string {
	_, list, ok := strings.Cut(request, "Workers of this conversation")
	if !ok {
		return "Nie znam stanu agentów."
	}
	var lines []string
	for _, line := range strings.Split(list, "\n")[1:] {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- worker-") {
			break
		}
		id, rest, _ := strings.Cut(strings.TrimPrefix(line, "- "), " ")
		state := "pracuje"
		switch {
		case strings.Contains(rest, ") failed"):
			state = "błąd (zerwane połączenie z modelem)"
		case strings.Contains(rest, ") done"):
			state = "skończył"
		}
		lines = append(lines, "- **"+id+"**: "+state)
	}
	return "Stan agentów:\n" + strings.Join(lines, "\n") + "\n\nNiczego nie wznawiam bez Twojej zgody; worker-2 możesz wznowić przyciskiem „Wznów”."
}

func TestDelegationPanelStatesAndStatusQuestionBrowser(t *testing.T) {
	shots := os.Getenv("SUPERCLI_UI_SHOTS")
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Second)
	t.Cleanup(cancel)
	dir := t.TempDir()
	if shots != "" {
		dir = filepath.Join(shots, "data")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("orchestrator = true\npreflight_repo = false\nbackground_wake_debounce_ms = 30\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &panelProvider{release: make(chan struct{})}
	defer close(p.release)
	eng.mu.Lock()
	eng.prov = p
	eng.mu.Unlock()
	srv := NewServer(eng, false)
	api := httptest.NewServer(srv.Handler())
	t.Cleanup(api.Close)
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 1280, Height: 960})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = browser.Close() })
	page, err := browser.NewPage(ctx, api.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := page.Screenshot(ctx); err != nil {
		t.Fatal(err)
	}
	app := &audit130App{t: t, ctx: ctx, srv: srv, api: api, browser: browser, page: page, dataDir: dir}
	app.viewport(1280, 960)
	app.waitReady()
	app.setLanguage("pl")
	shot := func(name string) {
		if shots == "" {
			return
		}
		app.eval(`(()=>{const t=document.querySelector('#toast');if(t){t.classList.remove('show');t.style.display='none';}return true;})()`, nil)
		app.settle()
		png, err := page.Screenshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(shots, name+".png")
		if err := os.WriteFile(path, png, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("screenshot: %s", path)
	}

	app.send("Rozpocznij pracę nad mobami, nocą i HUD.")
	app.waitJS("three delegation cards", `document.querySelectorAll('#stream .task-row').length>=3`, 20*time.Second)
	app.waitJS("states: working, error, finished",
		`(()=>{const s=document.querySelector('#worker-overview-summary').textContent;return s.includes('1 pracuje')&&s.includes('1 błąd')&&s.includes('1 gotowe');})()`, 20*time.Second)
	app.waitJS("turn finished", `!streaming`, 20*time.Second)

	var facts struct {
		Summary   string   `json:"summary"`
		Hints     []string `json:"hints"`
		Brackets  []string `json:"brackets"`
		HasStop   bool     `json:"hasStop"`
		HasResume bool     `json:"hasResume"`
		Cards     []string `json:"cards"`
	}
	app.eval(`(()=>{const rows=[...document.querySelectorAll('#stream .task-row')];
 return {summary:document.querySelector('#worker-overview-summary').textContent,
  hints:rows.map(r=>r.querySelector('.thint').textContent),
  brackets:[...document.querySelectorAll('#stream .event-line .tag')].map(n=>n.textContent).filter(s=>/\[|\]/.test(s)),
  hasStop:!!document.querySelector('#stream .task-row.running .worker-stop'),
  hasResume:!!document.querySelector('#stream .task-row.failed .worker-resume'),
  cards:rows.map(r=>r.className+' | '+r.querySelector('.tstat').textContent+' | '+(r._body?r._body.textContent.slice(0,160):''))};})()`, &facts)
	t.Logf("panel: %+v", facts)
	if len(facts.Brackets) > 0 {
		t.Fatalf("raw [tag] markers are visible: %v", facts.Brackets)
	}
	for _, hint := range facts.Hints {
		if strings.Contains(hint, "build-migration/tools/") || strings.Contains(hint, "Projekt:") {
			t.Fatalf("card shows a raw brief instead of its goal: %q", hint)
		}
	}
	if !facts.HasStop || !facts.HasResume {
		t.Fatalf("actions: stop on the working card=%v, resume on the failed card=%v", facts.HasStop, facts.HasResume)
	}
	app.eval(`(()=>{document.querySelector('#worker-overview').open=true;document.querySelectorAll('#stream .task-row').forEach(r=>{r.open=false;});window.scrollTo(0,0);return true;})()`, nil)
	shot("01-panel-stany-workerow")

	// The failed card, expanded: the error first, then Resume.
	// The automatic turn for the finished workers may rebuild the transcript;
	// wait for it to settle, then expand the (current) failed card.
	app.waitJS("automatic turn settled", `!streaming&&document.querySelector('#stream .task-row.failed')`, 20*time.Second)
	app.settle()
	app.waitJS("failed card expanded", `(()=>{const r=document.querySelector('#stream .task-row.failed');if(!r)return false;r.open=true;if(r._body)r._body.hidden=false;r.scrollIntoView({block:'center'});return r.open;})()`, 10*time.Second)
	app.waitJS("error outcome", `(()=>{const o=document.querySelector('#stream .task-row.failed .task-outcome.failed');return o&&o.textContent.includes('INTERNAL_ERROR');})()`, 10*time.Second)
	shot("02-rozwiniety-blad")

	// A status question while worker-1 still works: no new worker, no
	// resume, an answer built from the workers' real states.
	app.eval(`(()=>{const r=document.querySelector('#stream .task-row.failed');if(r)r.open=false;return true;})()`, nil)
	workersBefore := len(eng.workers.List())
	app.send("i jak idzie?")
	app.waitJS("status answer", `document.querySelector('#stream').textContent.includes('Stan agentów')&&!streaming`, 20*time.Second)
	if got := len(eng.workers.List()); got != workersBefore {
		t.Fatalf("the status question started a worker: %d -> %d", workersBefore, got)
	}
	if p.statusRefuse.Load() != 1 {
		t.Fatal("the model's resume on a status question was not refused")
	}
	var answer string
	app.eval(`(()=>{const m=[...document.querySelectorAll('#stream .msg-assistant')];return m[m.length-1].textContent;})()`, &answer)
	if !strings.Contains(answer, "worker-1") || !strings.Contains(answer, "pracuje") || !strings.Contains(answer, "błąd") {
		t.Fatalf("status answer lacks the real states: %q", answer)
	}
	for _, w := range eng.workers.List() {
		if s := w.Snapshot(); s.ID == "worker-2" && s.Runs != 1 {
			t.Fatalf("worker-2 was resumed by a status question: runs=%d", s.Runs)
		}
	}
	app.eval(`(()=>{const s=document.querySelector('#stream');s.scrollTop=s.scrollHeight;window.scrollTo(0,document.body.scrollHeight);return true;})()`, nil)
	shot("03-pytanie-i-jak-idzie")

	// Event lines carry a translated chip, never a raw "[stop]" marker.
	var line struct {
		Text string `json:"text"`
		Chip string `json:"chip"`
	}
	app.eval(`(()=>{const l=addEventLine(t('chat.stopped'),'','stop');addEventLine(t('chat.streamEnded'),'error','error');
 const s=document.querySelector('#stream');s.scrollTop=s.scrollHeight;return {text:l.textContent,chip:l.querySelector('.tag').textContent};})()`, &line)
	if strings.ContainsAny(line.Text, "[]") || line.Chip != "Zatrzymano" {
		t.Fatalf("stop line = %q (chip %q)", line.Text, line.Chip)
	}
	shot("04-komunikaty-bez-nawiasow")
}
