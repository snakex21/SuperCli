package webgui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

// Long-session stress (after 1.0.40). A synthetic session far above a
// 1M-token window - hundreds of turns, MB-sized tool outputs, screenshots,
// worker reports - is driven through the real GUI engine with a provider
// that enforces the window ("prompt is too long: X > Y", with the estimator
// undercounting like real models do):
//
//   - auto-compaction of the oversized history and recovery from a rejected
//     request;
//   - three background workers with hundreds of steps each, one stopped by a
//     usage limit and resumed by a plain "wznów je";
//   - an app restart in the middle (the session reloads from the database);
//   - a rewind to an earlier user message;
//   - the GUI transcript load (paged and full) with time and memory bounds.
//
// SUPERCLI_STRESS=full runs a larger variant; the default fits CI.

type stressSize struct {
	turns, bigEvery, bigBytes, imageEvery, workerSteps int
}

func stressSizes() stressSize {
	if os.Getenv("SUPERCLI_STRESS") == "full" {
		return stressSize{turns: 1200, bigEvery: 2, bigBytes: 2 << 20, imageEvery: 5, workerSteps: 280} // under the 300-step safety net
	}
	return stressSize{turns: 300, bigEvery: 3, bigBytes: 1 << 20, imageEvery: 10, workerSteps: 120}
}

const (
	stressWindow = 1_000_000
	stressRatio  = 1.25 // real tokens per estimated token
)

// stressProvider plays coordinator, workers and summarizer and enforces the
// window like Anthropic does.
type stressProvider struct {
	mu                sync.Mutex
	steps             int
	workerSteps       map[string]int // per worker (by its task prompt)
	limitHit          bool
	calls, rejected   int
	summaries         int
	maxAccepted       int
	limitWorker       string
	limitAt           int
	parentTurnSpawned bool
	resumeIssued      bool
	ratio             float64 // real tokens per estimated token
	limit             int     // the provider's real window
}

func (*stressProvider) Name() string { return "stress-model" }

func lastUserText(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleUser {
			return msgs[i].TextOnly().Content
		}
	}
	return ""
}

func (p *stressProvider) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	p.mu.Lock()
	ratio := p.ratio
	p.mu.Unlock()
	real := int(float64(llm.EstimateTokens(msgs)+llm.EstimateActiveImageTokens(msgs)) * ratio)
	parent := false
	for _, def := range defs {
		parent = parent || def.Name == "task"
	}
	summary := len(msgs) > 0 && msgs[0].Role == llm.RoleSystem && strings.Contains(msgs[0].Content, "Summarize the conversation")
	p.mu.Lock()
	p.calls++
	if real > p.limit {
		p.rejected++
		limit := p.limit
		p.mu.Unlock()
		return nil, fmt.Errorf(`http 400: {"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: %d tokens > %d maximum"}}`, real, limit)
	}
	p.maxAccepted = max(p.maxAccepted, real)
	if summary {
		p.summaries++
		p.mu.Unlock()
		return workerSteeringWireDeltas(llm.Delta{Content: "Goal: finish the survival night.\nDone: mobs and build.\nState: workers 1-3 active.\nPending: zombie spawn.", FinishReason: "stop"}), nil
	}
	if !parent {
		// A worker: one list_dir step per call until its step count is
		// reached, then its report. The task text names the worker's slot.
		task := ""
		for _, m := range msgs {
			if m.Role == llm.RoleUser {
				task = m.TextOnly().Content
				break
			}
		}
		slot := "w?"
		for _, s := range []string{"slot-1", "slot-2", "slot-3"} {
			if strings.Contains(task, s) {
				slot = s
			}
		}
		n := p.workerSteps[slot]
		if slot == p.limitWorker && n == p.limitAt && !p.limitHit {
			p.limitHit = true
			p.mu.Unlock()
			return nil, fmt.Errorf("anthropic: usage limit reached (HTTP 429): usage_limit_error")
		}
		p.workerSteps[slot] = n + 1
		total := p.steps
		p.mu.Unlock()
		if n < total {
			return workerSteeringWireCall(fmt.Sprintf("%s-step-%d", slot, n), "list_dir", map[string]any{"path": fmt.Sprintf("dirs/d%03d", n%200)})
		}
		return workerSteeringWireDeltas(llm.Delta{Content: "REPORT " + slot + ": zombie spawn verified", FinishReason: "stop"}), nil
	}
	user := lastUserText(msgs)
	spawn := !p.parentTurnSpawned && strings.Contains(user, "deleguj")
	resume := !p.resumeIssued && strings.Contains(user, "wznów je")
	if spawn {
		p.parentTurnSpawned = true
	}
	if resume {
		p.resumeIssued = true
	}
	p.mu.Unlock()
	// A tool result just returned: finish the turn with text.
	if len(msgs) > 0 && msgs[len(msgs)-1].Role == llm.RoleTool {
		return workerSteeringWireDeltas(llm.Delta{Content: "Done: delegated work is running.", FinishReason: "stop"}), nil
	}
	switch {
	case spawn:
		var deltas []llm.Delta
		for i := 1; i <= 3; i++ {
			args, _ := json.Marshal(map[string]any{"agent": "explore", "prompt": fmt.Sprintf("slot-%d: verify the zombie spawn rules, part %d", i, i), "async": true})
			deltas = append(deltas, llm.Delta{ToolCall: &llm.ToolCall{ID: fmt.Sprintf("spawn-%d", i), Name: "task", Arguments: string(args)}})
		}
		return workerSteeringWireDeltas(append(deltas, llm.Delta{FinishReason: "tool_calls"})...), nil
	case resume:
		return workerSteeringWireCall("resume-limited", "send_message", map[string]any{"to": "worker-2", "message": "The usage limit is back; continue from where you stopped."})
	}
	return workerSteeringWireDeltas(llm.Delta{Content: "Answer: " + strings.Join(strings.Fields(user), " ")[:min(40, len(user))], FinishReason: "stop"}), nil
}

// seedLongSession writes the synthetic history straight to the session store
// and returns the session id and the number of seeded messages.
func seedLongSession(t *testing.T, store *session.Store, home string, size stressSize) (string, int, map[int]int) {
	t.Helper()
	sess, err := store.Create(home, "stress-model", "stress: long Minecraft session")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	bigSeqs := map[int]int{} // seq -> content length of a big tool output
	seq := 0
	add := func(m llm.Message) {
		enc, err := session.FromMessage(m)
		if err != nil {
			t.Fatal(err)
		}
		got, err := store.AppendMessageWithSeq(ctx, sess.ID, enc)
		if err != nil {
			t.Fatal(err)
		}
		seq = got
	}
	// A 1x1 PNG per screenshot (pixels are not the point; the parts are).
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==")
	shot := func(i int) string {
		path := filepath.Join(home, "shots", fmt.Sprintf("screen-%d.png", i))
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, png, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	big := strings.Repeat("BUILD line: task :compileJava UP-TO-DATE, zombie spawn rule checked\n", size.bigBytes/60+1)[:size.bigBytes]
	for i := 0; i < size.turns; i++ {
		user := llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("turn %d: dodaj kolejną regułę spawnu zombie %s", i, strings.Repeat("szczegóły ", 40))}
		if i%size.imageEvery == 0 {
			user.Content = ""
			user.Parts = []llm.ContentPart{{Type: llm.PartTypeText, Text: fmt.Sprintf("turn %d: zrzut ekranu z gry", i)},
				{Type: llm.PartTypeImage, Image: &llm.ImageRef{ID: fmt.Sprintf("shot-%d", i), Name: fmt.Sprintf("screen-%d.png", i), MediaType: "image/png", Path: shot(i)}}}
		}
		add(user)
		id := fmt.Sprintf("seed-call-%d", i)
		add(llm.Message{Role: llm.RoleAssistant, Content: "Sprawdzam build.", ToolCalls: []llm.ToolCall{{ID: id, Name: "ctx_execute", Arguments: `{"command":["gradlew","build"]}`}}})
		out := strings.Repeat("BUILD SUCCESSFUL in 12s\n", 150)
		if i%size.bigEvery == 0 {
			out = big
		}
		add(llm.Message{Role: llm.RoleTool, Name: "ctx_execute", ToolCallID: id, Content: out})
		if i%size.bigEvery == 0 {
			// The store keeps at most session.MaxToolContentChars of one
			// output (by design); the check below compares with what it kept.
			stored, err := store.ReadMessageAt(ctx, sess.ID, seq)
			if err != nil {
				t.Fatal(err)
			}
			bigSeqs[seq] = len(stored.Content)
		}
		add(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("Reguła %d dodana, build przechodzi.", i)})
		if i%15 == 7 {
			add(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("<task-notification>\n<task-id>worker-%d</task-id>\n<status>completed</status>\n<result>%s</result>\n</task-notification>", 100+i, strings.Repeat("worker report line ", 300))})
			add(llm.Message{Role: llm.RoleAssistant, Content: "Raport workera przyjęty."})
		}
	}
	return sess.ID, seq, bigSeqs
}

func TestLongSessionStress(t *testing.T) {
	if testing.Short() {
		t.Skip("long-session stress skipped in -short")
	}
	started := time.Now()
	size := stressSizes()
	home, data := t.TempDir(), t.TempDir()
	for i := 0; i < 200; i++ {
		dir := filepath.Join(home, "dirs", fmt.Sprintf("d%03d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dir, "rule.txt"), []byte(fmt.Sprintf("rule %d\n", i)), 0o600)
	}
	cfg := fmt.Sprintf("orchestrator = true\npreflight_repo = false\nbackground_wake_debounce_ms = 30\ncontext_window = %d\n", stressWindow)
	if err := os.WriteFile(filepath.Join(data, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &stressProvider{limit: stressWindow, ratio: stressRatio, steps: size.workerSteps, workerSteps: map[string]int{}, limitWorker: "slot-2", limitAt: size.workerSteps / 2}
	open := func() *Engine {
		eng, err := NewEngine(echoConfig(), home, data)
		if err != nil {
			t.Fatal(err)
		}
		eng.mu.Lock()
		eng.prov = p
		eng.mu.Unlock()
		return eng
	}
	eng := open()
	defer func() { _ = eng.Close() }()
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sid, seeded, bigSeqs := seedLongSession(t, store, eng.Home(), size)
	var seededTokens int
	if all, err := store.ReadMessages(context.Background(), sid); err == nil {
		for _, enc := range all {
			if m, err := enc.ToMessage(); err == nil {
				seededTokens += llm.EstimateMessageTokens(m)
			}
		}
	}
	if real := int(float64(seededTokens) * stressRatio); real < 2*stressWindow {
		t.Fatalf("setup: seeded session is %d real tokens, want well above the %d window", real, stressWindow)
	}
	t.Logf("seeded %d messages, ~%d real tokens, in %s", seeded, int(float64(seededTokens)*stressRatio), time.Since(started).Round(time.Millisecond))

	turn := func(eng *Engine, prompt string) string {
		t.Helper()
		var answer strings.Builder
		var errs []string
		begin := time.Now()
		err := eng.runStream(context.Background(), prompt, sid, "", func(ev wireEvent) {
			switch ev.Type {
			case "message":
				answer.WriteString(ev.Text)
			case "error":
				errs = append(errs, ev.Text+ev.Err)
			}
		})
		if err != nil || len(errs) > 0 {
			t.Fatalf("turn %q failed: %v %q (calls=%d rejected=%d)", prompt, err, errs, p.calls, p.rejected)
		}
		if strings.TrimSpace(answer.String()) == "" {
			t.Fatalf("turn %q ended without a response", prompt)
		}
		t.Logf("turn %q: %s, answer %q", prompt, time.Since(begin).Round(time.Millisecond), clipStress(answer.String()))
		return answer.String()
	}

	// 1. The oversized session continues: compaction within the window.
	turn(eng, "kontynuuj, deleguj sprawdzenie reguł spawnu")
	if p.summaries == 0 {
		t.Fatal("the oversized history was not compacted")
	}
	// 2. Three background workers run their steps; one stops on the usage
	// limit (automatic wake turns report them meanwhile).
	waitStress(t, "workers finish or stop", 3*time.Minute, func() bool {
		done, failed := 0, 0
		for _, s := range eng.workers.List() {
			switch s.Snapshot().Status {
			case "done":
				done++
			case "failed":
				failed++
			}
		}
		return done == 2 && failed == 1
	}, func() string {
		var b strings.Builder
		for _, w := range eng.workers.List() {
			s := w.Snapshot()
			fmt.Fprintf(&b, "[%s %s steps=%d runs=%d err=%.120q]", s.ID, s.Status, s.Steps, s.Runs, s.LastError)
		}
		p.mu.Lock()
		fmt.Fprintf(&b, " provider steps=%v calls=%d", p.workerSteps, p.calls)
		p.mu.Unlock()
		return b.String()
	})
	limited := eng.workers.List()
	var w2 *agent.Worker
	for _, w := range limited {
		if w.ID == "worker-2" {
			w2 = w
		}
	}
	if w2 == nil || !strings.Contains(w2.Snapshot().LastError, "usage limit") {
		t.Fatalf("worker-2 did not stop on the usage limit: %+v", w2)
	}
	waitIdle(t, eng)

	// 3. Restart in the middle: the session reloads from the database.
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	eng = open()
	turn(eng, "limit wrócił, wznów je")
	waitStress(t, "worker-2 resumes and finishes", 3*time.Minute, func() bool {
		for _, w := range eng.workers.List() {
			if w.ID == "worker-2" {
				s := w.Snapshot()
				return s.Status == "done" && s.Runs >= 2
			}
		}
		return false
	})
	waitIdle(t, eng)
	turn(eng, "dalej, podsumuj stan")

	// 4. Every request stayed within the window; rejections were recovered.
	if p.maxAccepted > stressWindow {
		t.Fatalf("a request above the window was accepted: %d", p.maxAccepted)
	}
	t.Logf("provider: calls=%d rejected=%d summaries=%d max-accepted=%d tokens", p.calls, p.rejected, p.summaries, p.maxAccepted)

	// 5. No data loss: the seeded transcript is intact in the database.
	store, err = eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	for seq, n := range bigSeqs {
		enc, err := store.ReadMessageAt(context.Background(), sid, seq)
		if err != nil || len(enc.Content) != n {
			t.Fatalf("big tool output at seq %d: len %d err %v, want %d", seq, len(enc.Content), err, n)
		}
	}
	after, err := store.ReadMessages(context.Background(), sid)
	if err != nil || len(after) < seeded {
		t.Fatalf("transcript has %d messages after the run, seeded %d (err %v)", len(after), seeded, err)
	}

	// 6. GUI transcript load: paged (what the GUI uses) and full.
	srv := NewServer(eng, false)
	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for _, query := range []string{"&limit=100", ""} {
		begin := time.Now()
		req := httptest.NewRequest(http.MethodGet, "/api/transcript?id="+sid+query, nil)
		req.Host, req.RemoteAddr = "127.0.0.1:1234", "127.0.0.1:5555"
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		took := time.Since(begin)
		if rec.Code != http.StatusOK {
			t.Fatalf("transcript%s: status %d: %s", query, rec.Code, clipStress(rec.Body.String()))
		}
		t.Logf("transcript%s: %d bytes in %s", query, rec.Body.Len(), took.Round(time.Millisecond))
		if query != "" && (rec.Body.Len() > 8<<20 || took > 5*time.Second) {
			t.Fatalf("paged transcript load too heavy: %d bytes in %s", rec.Body.Len(), took)
		}
		if took > 30*time.Second {
			t.Fatalf("full transcript load took %s", took)
		}
	}
	var afterMem runtime.MemStats
	runtime.ReadMemStats(&afterMem)
	if grown := int64(afterMem.HeapInuse) - int64(before.HeapInuse); grown > 1<<30 {
		t.Fatalf("transcript load kept %d MB in use", grown>>20)
	}

	// 7. Rewind to the restart turn's user message.
	seq, msgID := lastUserSeq(t, store, sid, "limit wrócił")
	body := fmt.Sprintf(`{"session_id":%q,"selected_seq":%d,"selected_message_id":"%d","rewind_files":false}`, sid, seq, msgID)
	req := httptest.NewRequest(http.MethodPost, "/api/session/rewind", strings.NewReader(body))
	req.Host, req.RemoteAddr = "127.0.0.1:1234", "127.0.0.1:5555"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rewind: status %d: %s", rec.Code, clipStress(rec.Body.String()))
	}
	for seq, n := range bigSeqs {
		if enc, err := store.ReadMessageAt(context.Background(), sid, seq); err != nil || len(enc.Content) != n {
			t.Fatalf("rewind lost seeded history at seq %d", seq)
		}
	}
	turn(eng, "po cofnięciu: kontynuuj")

	// 8. A model whose real window is smaller than configured: the first
	// request is rejected with "prompt is too long", the limit is learned, the
	// history compacts within it and the turn still gets its answer.
	near, err := store.Create(eng.Home(), "stress-model", "stress: near the window")
	if err != nil {
		t.Fatal(err)
	}
	threshold := agent.AutoCompactThreshold(stressWindow)
	chunk := strings.Repeat("zombie spawn rule evidence line\n", 1500)
	for est, i := 0, 0; est < threshold/24; i++ {
		for _, m := range []llm.Message{
			{Role: llm.RoleUser, Content: fmt.Sprintf("near turn %d", i)},
			{Role: llm.RoleAssistant, Content: fmt.Sprintf("analysis %d: ", i) + strings.ReplaceAll(chunk, "line", fmt.Sprintf("line-%d", i))},
		} {
			enc, _ := session.FromMessage(m)
			if err := store.AppendMessage(context.Background(), near.ID, enc); err != nil {
				t.Fatal(err)
			}
			est += llm.EstimateMessageTokens(m)
		}
	}
	rejectedBefore := p.rejected
	p.mu.Lock()
	// The model's real window is far smaller than configured.
	p.limit = 30_000
	p.mu.Unlock()
	sid = near.ID
	turn(eng, "sprawdź pliki w repo i popraw kod reguł spawnu przy granicy okna")
	if p.rejected == rejectedBefore {
		t.Fatal("the near-window session was never rejected: the overflow path was not exercised")
	}
	t.Logf("near-window session: %d rejected request(s), recovered", p.rejected-rejectedBefore)
	t.Logf("long-session stress finished in %s", time.Since(started).Round(time.Millisecond))
}

func lastUserSeq(t *testing.T, store *session.Store, sid, prefix string) (int, int64) {
	t.Helper()
	all, err := store.ReadMessages(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Role == string(llm.RoleUser) && strings.HasPrefix(all[i].Content, prefix) {
			msg, err := store.ReadTranscriptMessageAt(context.Background(), sid, all[i].Seq)
			if err != nil {
				t.Fatal(err)
			}
			return all[i].Seq, msg.MessageID
		}
	}
	t.Fatalf("no user message starting with %q", prefix)
	return 0, 0
}

func waitStress(t *testing.T, what string, within time.Duration, ok func() bool, state ...func() string) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !ok() {
		if time.Now().After(deadline) {
			detail := ""
			for _, f := range state {
				detail += " " + f()
			}
			t.Fatalf("timed out: %s%s", what, detail)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitIdle waits for automatic wake turns to finish.
func waitIdle(t *testing.T, eng *Engine) {
	t.Helper()
	waitStress(t, "conversation idle", time.Minute, func() bool { return !eng.HasActiveWork() })
}

func clipStress(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
