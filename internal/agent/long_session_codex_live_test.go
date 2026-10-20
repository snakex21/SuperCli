package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/account/codexauth"
	"supercli/internal/llm"
	"supercli/internal/storage/memory"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

// Explicit, paid/subscription opt-in. This test works only on a separately
// prepared fixture. Normal go test never contacts any configured provider.
type longCodexFixture struct {
	SourceSession   string `json:"source_session"`
	Cwd             string `json:"cwd"`
	Model           string `json:"model"`
	Provider        string `json:"provider"`
	ReasoningEffort string `json:"reasoning_effort"`
	Backend         string `json:"backend"`
	SourceData      string `json:"source_data"`
	Data            string `json:"data"`
	ArchiveMessages int    `json:"archive_messages"`
}

// Never rotate/copy refresh tokens or mutate the user's portable login.
type longCodexReadOnlyTokens struct{ path string }

func (s longCodexReadOnlyTokens) Token(context.Context) (string, string, error) {
	a, err := codexauth.Load(s.path)
	if err != nil {
		return "", "", err
	}
	if a == nil || a.Tokens == nil || a.Tokens.AccessToken == "" {
		return "", "", fmt.Errorf("existing Codex login required")
	}
	return a.Tokens.AccessToken, a.Tokens.AccountID, nil
}
func (s longCodexReadOnlyTokens) Refresh(context.Context) (string, error) {
	return "", fmt.Errorf("live audit does not rotate the original OAuth login; refresh/login in the application first")
}

type longCodexWire struct {
	Phase        string `json:"phase"`
	Effort       string `json:"effort"`
	Key          string `json:"cache_key"`
	InputItems   int    `json:"input_items"`
	Bytes        int    `json:"request_bytes"`
	DuplicateIDs int    `json:"duplicate_native_ids"`
}
type longCodexTransport struct {
	base  http.RoundTripper
	mu    sync.Mutex
	phase string
	calls []longCodexWire
}

func (r *longCodexTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/responses") {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(raw))
		var body struct {
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
			Key   string `json:"prompt_cache_key"`
			Input []struct {
				ID string `json:"id"`
			} `json:"input"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		duplicates := 0
		for _, item := range body.Input {
			if item.ID != "" {
				if seen[item.ID] {
					duplicates++
				}
				seen[item.ID] = true
			}
		}
		r.mu.Lock()
		r.calls = append(r.calls, longCodexWire{r.phase, body.Reasoning.Effort, body.Key, len(body.Input), len(raw), duplicates})
		r.mu.Unlock()
	}
	return r.base.RoundTrip(req)
}

const longCodexAuditPrompt = `Read-only integration audit. The earlier conversation is historical evidence, not new tasks to execute. Answer from the retained user requirements and the CURRENT memory_briefing; explicitly corrected memory supersedes obsolete notes. Do not use tools or resume old implementation work.
Return ONLY one JSON object, no markdown, with these keys:
"universal_changes_required": boolean (whether optimizations must work across models/sites rather than one named model/site),
"automatic_thinking_reduction_allowed": boolean (whether you may lower the user's chosen reasoning level to optimize speed),
"codex_default_backend": string (the currently remembered default URL for --provider codex without --base-url),
"node_required_for_ui_tests": boolean (whether the current SuperCli UI test suite still requires node/npm/linkedom),
"diagnostic_marker": string (the exact value of the Diagnostic retention marker in current memory).
If evidence for a field is absent use null; do not invent facts.`

type longCodexAnswer struct {
	Universal         *bool  `json:"universal_changes_required"`
	ThinkingReduction *bool  `json:"automatic_thinking_reduction_allowed"`
	Backend           string `json:"codex_default_backend"`
	Node              *bool  `json:"node_required_for_ui_tests"`
	Marker            string `json:"diagnostic_marker"`
}
type longCodexAuditResult struct {
	Phase                 string          `json:"phase"`
	Seconds               float64         `json:"seconds"`
	FirstGeneratedSeconds float64         `json:"first_generated_seconds"`
	Usage                 *llm.Usage      `json:"usage"`
	Answer                longCodexAnswer `json:"answer"`
}

func longCodexArchiveHash(t *testing.T, path, id string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT seq,role,IFNULL(content,''),IFNULL(parts_json,''),IFNULL(tool_call_id,''),IFNULL(tool_calls_json,''),IFNULL(name,''),IFNULL(origin_json,'') FROM messages WHERE session_id=? ORDER BY seq`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	h := sha256.New()
	enc := json.NewEncoder(h)
	for rows.Next() {
		var seq int
		var cols [7]string
		if err := rows.Scan(&seq, &cols[0], &cols[1], &cols[2], &cols[3], &cols[4], &cols[5], &cols[6]); err != nil {
			t.Fatal(err)
		}
		if err := enc.Encode([]any{seq, cols}); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestLongSessionCodexRestartCompactionLive(t *testing.T) {
	if os.Getenv("SUPERCLI_LIVE_CODEX_LONG") != "1" {
		t.Skip("explicit live Codex long-session fixture opt-in")
	}
	stage := os.Getenv("SUPERCLI_LIVE_CODEX_STAGE")
	if stage == "" {
		t.Fatal("isolated stage required")
	}
	raw, err := os.ReadFile(filepath.Join(stage, "fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f longCodexFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	stage, err = filepath.Abs(stage)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(stage, f.Data)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.Clean(f.Data) == filepath.Clean(f.SourceData) {
		t.Fatal("fixture must remain inside isolated stage")
	}
	if f.Provider != "codex" || f.ArchiveMessages < 10000 {
		t.Fatal("expected original long Codex session")
	}
	oldEffort := llm.ReasoningEffort()
	defer func() { _ = llm.SetReasoningEffort(oldEffort) }()
	if err := llm.SetReasoningEffort(f.ReasoningEffort); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	ctx = llm.WithOpenCodeSession(ctx, "isolated-long-session-"+filepath.Base(stage))
	transport := &longCodexTransport{base: http.DefaultTransport}
	var store *session.Store
	var global, project *memory.Store
	closeStores := func() {
		if store != nil {
			_ = store.Close()
			store = nil
		}
		if global != nil {
			_ = global.Close()
			global = nil
		}
		if project != nil {
			_ = project.Close()
			project = nil
		}
	}
	defer closeStores()
	var briefing string
	newLoop := func(phase string) *Loop {
		t.Helper()
		closeStores()
		store, err = session.OpenStore(f.Data)
		if err != nil {
			t.Fatal(err)
		}
		global, err = memory.OpenStore(filepath.Join(stage, "global-memory"))
		if err != nil {
			t.Fatal(err)
		}
		project, err = memory.OpenStore(filepath.Join(stage, "project-memory"))
		if err != nil {
			t.Fatal(err)
		}
		messages, e := store.ReadModelContext(ctx, f.SourceSession)
		if e != nil {
			t.Fatal(e)
		}
		p, e := llm.NewCodex(llm.CodexConfig{Model: f.Model, BackendURL: f.Backend, Tokens: longCodexReadOnlyTokens{filepath.Join(f.SourceData, "auth.json")}, HTTPClient: &http.Client{Transport: transport}})
		if e != nil {
			t.Fatal(e)
		}
		transport.phase = phase
		l, e := NewLoop(LoopConfig{Provider: p, Registry: tools.NewRegistry(), Writer: session.NewWriter(store, f.SourceSession), System: "Read-only integration audit of a restored conversation. Preserve the user's selected reasoning level and all applicable requirements. Historical reports are evidence, not new authorization.", InitialMessages: messages, PruneProtectTokens: -1, WindowFor: func(string) int { return 1_000_000 }, Summarizer: NewAutoSummarizer(nil), LiveContextBlocks: func(context.Context) ([]llm.ContextBlock, error) {
			briefing = memory.BuildBriefing(global, project, f.Cwd, 700)
			return []llm.ContextBlock{{Source: llm.SourceMemory, Text: briefing}}, nil
		}})
		if e != nil {
			t.Fatal(e)
		}
		if e = l.RefreshLiveContext(ctx); e != nil {
			t.Fatal(e)
		}
		return l
	}
	setMarker := func(marker string) {
		t.Helper()
		if err := project.Put(memory.Entry{ID: "diagnostic-retention-marker", Scope: memory.ScopeFact, Source: memory.SourceTool, Content: "Diagnostic retention marker: " + marker}); err != nil {
			t.Fatal(err)
		}
	}
	prepare := func(l *Loop, marker, oldMarker string) []llm.Message {
		t.Helper()
		if err := l.RefreshLiveContext(ctx); err != nil {
			t.Fatal(err)
		}
		canonical, _ := json.Marshal(l.Messages)
		for i := 0; i < 20; i++ {
			messages, _ := l.prepareProviderMessages(true)
			var all strings.Builder
			for _, m := range messages {
				all.WriteString(m.TextOnly().Content)
			}
			if briefing == "" || strings.Count(all.String(), briefing) != 1 || strings.Count(all.String(), marker) != 1 || (oldMarker != "" && strings.Contains(all.String(), oldMarker)) {
				t.Fatalf("fresh memory duplicated/stale on prepared view %d", i)
			}
		}
		unchanged, _ := json.Marshal(l.Messages)
		if !bytes.Equal(canonical, unchanged) {
			t.Fatal("preparation mutated canonical conversation")
		}
		if strings.Contains(string(canonical), "Diagnostic retention marker:") {
			t.Fatal("live memory leaked into saved history")
		}
		messages, _ := l.prepareProviderMessages(true)
		return messages
	}
	var results []longCodexAuditResult
	audit := func(l *Loop, phase, marker string, messages []llm.Message) {
		t.Helper()
		transport.phase = phase
		messages = append(messages, llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceHuman, Text: longCodexAuditPrompt}}))
		start := time.Now()
		ch, e := l.provider.Complete(llm.WithPurpose(ctx, llm.PurposeMain), messages, nil)
		if e != nil {
			t.Fatal(e)
		}
		var text strings.Builder
		result := longCodexAuditResult{Phase: phase}
		for d := range ch {
			if d.Err != nil {
				t.Fatal(d.Err)
			}
			if d.ToolCall != nil {
				t.Fatal("read-only audit unexpectedly requested a tool")
			}
			if result.FirstGeneratedSeconds == 0 && (d.Content != "" || d.ReasoningStarted || d.OutputStarted || d.Reasoning != "") {
				result.FirstGeneratedSeconds = time.Since(start).Seconds()
			}
			text.WriteString(d.Content)
			if d.Usage != nil {
				u := *d.Usage
				result.Usage = &u
			}
		}
		result.Seconds = time.Since(start).Seconds()
		body := strings.TrimSpace(text.String())
		body = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(body, "```json"), "```"), "```"))
		if err := json.Unmarshal([]byte(body), &result.Answer); err != nil {
			t.Fatalf("audit returned invalid JSON (%d bytes): %v", len(body), err)
		}
		a := result.Answer
		if a.Universal == nil || !*a.Universal || a.ThinkingReduction == nil || *a.ThinkingReduction || a.Node == nil || *a.Node || strings.TrimRight(a.Backend, "/") != "https://chatgpt.com/backend-api/codex" || a.Marker != marker {
			t.Fatalf("incorrect restored agreements in %s: %+v", phase, a)
		}
		results = append(results, result)
		t.Logf("%s: correct current agreements; %.2fs first generated %.2fs", phase, result.Seconds, result.FirstGeneratedSeconds)
	}
	archiveBefore := longCodexArchiveHash(t, filepath.Join(f.Data, "sessions.db"), f.SourceSession)
	l := newLoop("before-restart")
	setMarker("isolated-marker-version-1")
	// Real close/reopen of DB and new Loop/provider, not just an in-memory clone.
	l = newLoop("restart-before-compaction")
	before := cloneCompactPrefix(l.Messages)
	beforeMessages := len(before)
	audit(l, "restart-before-compaction", "isolated-marker-version-1", prepare(l, "isolated-marker-version-1", ""))
	history := l.compactionHistory()
	split := history.originalSplit(completedCompactSplit(history, l.windowResolution().Tokens))
	suffix := cloneCompactPrefix(l.Messages[split:])
	expectedCarry := compactPreservedUserContext(l.compactionHistory().messages[:completedCompactSplit(l.compactionHistory(), l.windowResolution().Tokens)])
	transport.phase = "compact"
	started := time.Now()
	event, e := l.CompactNow(ctx)
	if e != nil {
		t.Fatal(e)
	}
	compactSeconds := time.Since(started).Seconds()
	if event.Removed <= 0 {
		t.Fatal("real compaction removed no prefix")
	}
	compacted := cloneCompactPrefix(l.Messages)
	if !reflect.DeepEqual(compacted[len(compacted)-len(suffix):], suffix) {
		t.Fatal("active tail changed during compaction")
	}
	i := leadingSystemCount(compacted)
	if !strings.Contains(compacted[i].Content, expectedCarry) {
		t.Fatal("exact retained user context missing after real summary")
	}
	setMarker("isolated-marker-version-2")
	l = newLoop("restart-after-compaction")
	if !reflect.DeepEqual(l.Messages, compacted) {
		t.Fatal("restart changed/duplicated compacted projection")
	}
	audit(l, "restart-after-compaction", "isolated-marker-version-2", prepare(l, "isolated-marker-version-2", "isolated-marker-version-1"))
	canonical, _ := json.Marshal(l.Messages)
	l = newLoop("second-restart")
	prepare(l, "isolated-marker-version-2", "isolated-marker-version-1")
	again, _ := json.Marshal(l.Messages)
	if !bytes.Equal(canonical, again) {
		t.Fatal("second restart changed canonical history")
	}
	if archiveBefore != longCodexArchiveHash(t, filepath.Join(f.Data, "sessions.db"), f.SourceSession) {
		t.Fatal("raw archive modified by audit or compaction")
	}
	if llm.ReasoningEffort() != f.ReasoningEffort {
		t.Fatal("selected thinking changed")
	}
	transport.mu.Lock()
	wires := append([]longCodexWire(nil), transport.calls...)
	transport.mu.Unlock()
	if len(wires) != 3 {
		t.Fatalf("unexpected HTTP work: %d calls", len(wires))
	}
	for _, w := range wires {
		if w.Effort != f.ReasoningEffort || w.DuplicateIDs != 0 {
			t.Fatalf("wire reasoning/duplicates mismatch: %+v", w)
		}
	}
	if wires[0].Key == "" || wires[0].Key != wires[2].Key || wires[0].Key == wires[1].Key {
		t.Fatal("cache identity changed across restart or helper shared main cache")
	}
	report := map[string]any{"model": f.Model, "effort": f.ReasoningEffort, "archive_messages": f.ArchiveMessages, "before_messages": beforeMessages, "after_messages": len(compacted), "compact_seconds": compactSeconds, "exact_user_carry_preserved": true, "active_tail_preserved": true, "archive_unchanged": true, "prepared_views_per_restart": 20, "audits": results, "wire": wires}
	encoded, e := json.MarshalIndent(report, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(stage, "live-result.json"), encoded, 0600); e != nil {
		t.Fatal(e)
	}
	t.Logf("LIVE: %d archive rows; %d -> %d model messages; compact %.2fs; exact user carry and active tail preserved; three HTTP requests, same thinking; two cold reopen checks", f.ArchiveMessages, beforeMessages, len(compacted), compactSeconds)
}

// Run in a separate go test invocation AFTER the test above has terminated.
// No loop, provider, capability singleton or in-memory projection is inherited.
func TestLongSessionCodexColdProcessRestoreLive(t *testing.T) {
	if os.Getenv("SUPERCLI_LIVE_CODEX_COLD_PROCESS") != "1" {
		t.Skip("explicit cold-process live Codex opt-in")
	}
	stage := os.Getenv("SUPERCLI_LIVE_CODEX_STAGE")
	raw, err := os.ReadFile(filepath.Join(stage, "fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f longCodexFixture
	if err = json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(stage, f.Data)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.Clean(f.Data) == filepath.Clean(f.SourceData) {
		t.Fatal("isolated data required")
	}
	raw, err = os.ReadFile(filepath.Join(stage, "live-result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var previous struct {
		After int             `json:"after_messages"`
		Wire  []longCodexWire `json:"wire"`
	}
	if err = json.Unmarshal(raw, &previous); err != nil || len(previous.Wire) != 3 {
		t.Fatal("previous live test result required")
	}
	old := llm.ReasoningEffort()
	defer func() { _ = llm.SetReasoningEffort(old) }()
	if err = llm.SetReasoningEffort(f.ReasoningEffort); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	ctx = llm.WithOpenCodeSession(ctx, "isolated-long-session-"+filepath.Base(stage))
	archive := longCodexArchiveHash(t, filepath.Join(f.Data, "sessions.db"), f.SourceSession)
	store, err := session.OpenStore(f.Data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	global, err := memory.OpenStore(filepath.Join(stage, "global-memory"))
	if err != nil {
		t.Fatal(err)
	}
	defer global.Close()
	project, err := memory.OpenStore(filepath.Join(stage, "project-memory"))
	if err != nil {
		t.Fatal(err)
	}
	defer project.Close()
	messages, err := store.ReadModelContext(ctx, f.SourceSession)
	if err != nil {
		t.Fatal(err)
	}
	transport := &longCodexTransport{base: http.DefaultTransport, phase: "cold-process-restore"}
	p, err := llm.NewCodex(llm.CodexConfig{Model: f.Model, BackendURL: f.Backend, Tokens: longCodexReadOnlyTokens{filepath.Join(f.SourceData, "auth.json")}, HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	briefing := memory.BuildBriefing(global, project, f.Cwd, 700)
	l, err := NewLoop(LoopConfig{Provider: p, Registry: tools.NewRegistry(), Writer: session.NewWriter(store, f.SourceSession), System: "Read-only integration audit of a restored conversation. Preserve the user's selected reasoning level and all applicable requirements. Historical reports are evidence, not new authorization.", InitialMessages: messages, PruneProtectTokens: -1, LiveContextBlocks: func(context.Context) ([]llm.ContextBlock, error) {
		return []llm.ContextBlock{{Source: llm.SourceMemory, Text: briefing}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Messages) != previous.After {
		t.Fatal("cold process lost/duplicated projection messages")
	}
	if err = l.RefreshLiveContext(ctx); err != nil {
		t.Fatal(err)
	}
	canonical, _ := json.Marshal(l.Messages)
	for i := 0; i < 20; i++ {
		prepared, _ := l.prepareProviderMessages(true)
		text := liveRequestText(prepared)
		if briefing == "" || strings.Count(text, briefing) != 1 || strings.Count(text, "isolated-marker-version-2") != 1 || strings.Contains(text, "isolated-marker-version-1") {
			t.Fatal("cold process supplied duplicated/stale memory")
		}
	}
	prepared, _ := l.prepareProviderMessages(true)
	prepared = append(prepared, llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceHuman, Text: longCodexAuditPrompt}}))
	start := time.Now()
	ch, err := p.Complete(llm.WithPurpose(ctx, llm.PurposeMain), prepared, nil)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	result := longCodexAuditResult{Phase: "cold-process-restore"}
	for d := range ch {
		if d.Err != nil {
			t.Fatal(d.Err)
		}
		if d.ToolCall != nil {
			t.Fatal("cold read-only audit requested a tool")
		}
		if result.FirstGeneratedSeconds == 0 && (d.Content != "" || d.ReasoningStarted || d.OutputStarted || d.Reasoning != "") {
			result.FirstGeneratedSeconds = time.Since(start).Seconds()
		}
		text.WriteString(d.Content)
		if d.Usage != nil {
			u := *d.Usage
			result.Usage = &u
		}
	}
	result.Seconds = time.Since(start).Seconds()
	body := strings.TrimSpace(text.String())
	body = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(body, "```json"), "```"), "```"))
	if err = json.Unmarshal([]byte(body), &result.Answer); err != nil {
		t.Fatal(err)
	}
	a := result.Answer
	if a.Universal == nil || !*a.Universal || a.ThinkingReduction == nil || *a.ThinkingReduction || a.Node == nil || *a.Node || strings.TrimRight(a.Backend, "/") != "https://chatgpt.com/backend-api/codex" || a.Marker != "isolated-marker-version-2" {
		t.Fatalf("cold process lost current agreement: %+v", a)
	}
	transport.mu.Lock()
	wires := append([]longCodexWire(nil), transport.calls...)
	transport.mu.Unlock()
	if len(wires) != 1 || wires[0].Effort != f.ReasoningEffort || wires[0].Key != previous.Wire[2].Key || wires[0].DuplicateIDs != 0 {
		t.Fatal("cold-process wire changed reasoning/cache identity or duplicated native state")
	}
	again, _ := json.Marshal(l.Messages)
	if !bytes.Equal(canonical, again) || strings.Contains(string(again), "Diagnostic retention marker:") || archive != longCodexArchiveHash(t, filepath.Join(f.Data, "sessions.db"), f.SourceSession) {
		t.Fatal("cold read added context to canonical history or modified archive")
	}
	report := map[string]any{"process_id": os.Getpid(), "model": f.Model, "effort": f.ReasoningEffort, "canonical_messages": len(l.Messages), "prepared_views": 20, "actual_cold_process": true, "result": result, "wire": wires, "archive_unchanged": true}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(stage, "cold-process-result.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("NEW PROCESS: %d messages; correct current agreements; one current memory snapshot; same %s; %.2fs total / %.2fs first generated", len(l.Messages), f.ReasoningEffort, result.Seconds, result.FirstGeneratedSeconds)
}
