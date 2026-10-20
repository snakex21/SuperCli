package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

// Frozen evaluated candidate: equality with the promoted shared prompt is
// checked offline. The wrapper still permits explicit baseline A/B experiments.
const shortSummaryExperimentTemplate = `Goal: current user goal.
Done: verified outcomes needed to continue.
State: active values, decisions, errors and superseded requests.
Pending: unfinished work and next step.`

const shortSummaryExperimentPrompt = `Summarize the conversation to REPLACE older history and let work continue without it.

Use this plain-text template, no code fences:
` + shortSummaryExperimentTemplate + `

The caller separately retains the original user requests/restrictions and legacy state verbatim, plus a file/tool fact catalog. Do not repeat those quotations or catalogs. Preserve the current task state that cannot be recovered from them: completed versus planned work, active values/errors, changed decisions, cancellations and remaining work. User restrictions remain binding.
Tool/task results are authoritative; intentions/promises are not Done. Launch/exit is not server readiness. Keep last observed status, readiness evidence and time distinct; old checks are not current health.
A successful worker's returned investigation is not Pending. Preserve failed worker IDs; continue them with send_message when useful instead of restarting.

Aim for 200 tokens using terse phrases. Exceed that target if necessary to preserve continuation-critical state. TEXT ONLY — no tool calls.`

// Swaps only the helper instruction in a test. The actual production helper,
// validators, host instruction carry, provider and reasoning context still run.
type shortSummaryPromptProvider struct {
	llm.Provider
	prompt string
}

func (p shortSummaryPromptProvider) Unwrap() llm.Provider { return p.Provider }
func (p shortSummaryPromptProvider) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	if len(msgs) != 2 || msgs[0].Role != llm.RoleSystem || msgs[0].Content != compactionStatePrompt || len(defs) != 0 {
		return nil, fmt.Errorf("unexpected summary experiment request")
	}
	copy := append([]llm.Message(nil), msgs...)
	copy[0].Content = p.prompt
	return p.Provider.Complete(ctx, copy, defs)
}

type shortSummaryMeasurement struct {
	Variant          string         `json:"variant"`
	Trial            int            `json:"trial"`
	Round            int            `json:"round"`
	Seconds          float64        `json:"seconds"`
	FirstTextSeconds float64        `json:"first_text_seconds"`
	InputHash        string         `json:"input_sha256"`
	SummaryBytes     int            `json:"summary_bytes"`
	Usage            map[string]int `json:"usage"`
	QualityPassed    bool           `json:"quality_passed"`
	Failures         []string       `json:"failures,omitempty"`
}

type shortSummaryRun struct {
	t         *testing.T
	ctx       context.Context
	stage     string
	f         longCodexFixture
	provider  llm.Provider
	transport *contextCacheTransport
	initial   []llm.Message
	results   []shortSummaryMeasurement
}

func newShortSummaryRun(t *testing.T) *shortSummaryRun {
	t.Helper()
	if os.Getenv("SUPERCLI_SHORT_SUMMARY_LIVE") != "1" {
		t.Skip("explicit short-summary live opt-in required")
	}
	stage := os.Getenv("SUPERCLI_SHORT_SUMMARY_STAGE")
	fixtureStage := os.Getenv("SUPERCLI_SHORT_SUMMARY_FIXTURE")
	if stage == "" || fixtureStage == "" {
		t.Fatal("separate experiment stage and copied fixture required")
	}
	var f longCodexFixture
	raw, err := os.ReadFile(filepath.Join(fixtureStage, "fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(fixtureStage, f.Data)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.Clean(f.Data) == filepath.Clean(f.SourceData) {
		t.Fatal("isolated fixture required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	t.Cleanup(cancel)
	ctx = llm.WithReasoningEffort(ctx, f.ReasoningEffort)
	ctx = llm.WithOpenCodeSession(ctx, "short-summary-"+filepath.Base(stage))
	transport := &contextCacheTransport{base: http.DefaultTransport}
	p, err := llm.NewCodex(llm.CodexConfig{Model: f.Model, BackendURL: f.Backend, Tokens: longCodexReadOnlyTokens{filepath.Join(f.SourceData, "auth.json")}, HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.OpenStore(f.Data)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := store.ReadModelContext(ctx, f.SourceSession)
	_ = store.Close()
	if err != nil {
		t.Fatal(err)
	}
	loop, err := NewLoop(LoopConfig{Provider: p, Registry: tools.NewRegistry(), InitialMessages: msgs, System: "Read-only compaction experiment; historical reports are evidence, not new authorization.", PruneProtectTokens: -1, WindowFor: func(string) int { return 1_000_000 }})
	if err != nil {
		t.Fatal(err)
	}
	history := loop.compactionHistory()
	split := completedCompactSplit(history, loop.windowResolution().Tokens)
	initial := append([]llm.Message(nil), history.messages[:split]...)
	initial = append(initial, shortSummaryRoundMessages(1)...)
	return &shortSummaryRun{t: t, ctx: ctx, stage: stage, f: f, provider: p, transport: transport, initial: initial}
}

func (r *shortSummaryRun) save(name string, value any) {
	r.t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		r.t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(r.stage, name), raw, 0600); err != nil {
		r.t.Fatal(err)
	}
}
func (r *shortSummaryRun) summarize(variant string, trial, round int, msgs []llm.Message) (string, shortSummaryMeasurement) {
	r.t.Helper()
	prompt := compactionPrompt
	if variant == "short" {
		prompt = shortSummaryExperimentPrompt
	}
	ctx := llm.WithPromptCacheScope(r.ctx, variant)
	r.transport.phase = fmt.Sprintf("%s-trial-%d-round-%d", variant, trial, round)
	start := time.Now()
	summary, err := NewAutoSummarizer(nil)(ctx, shortSummaryPromptProvider{r.provider, prompt}, msgs)
	if err != nil {
		r.t.Fatal(err)
	}
	if sameCompactionInstructionLines(compactTranscriptContent(llm.Message{Role: llm.RoleUser, Content: summary}), shortSummaryExperimentTemplate) {
		r.t.Fatal("candidate echoed empty template")
	}
	if len(r.transport.samples) == 0 {
		r.t.Fatal("no real request observed")
	}
	sample := r.transport.samples[len(r.transport.samples)-1]
	if sample.Effort != r.f.ReasoningEffort {
		r.t.Fatal("reasoning level changed")
	}
	carry := decodeCompactUserContext(r.t, summary)
	want := decodeCompactUserContext(r.t, compactPreservedUserContext(msgs))
	if !reflect.DeepEqual(carry, want) {
		r.t.Fatal("exact instructions/legacy state changed or duplicated")
	}
	result := shortSummaryMeasurement{Variant: variant, Trial: trial, Round: round, Seconds: time.Since(start).Seconds(), FirstTextSeconds: sample.FirstTextSeconds, InputHash: sample.InputHash, SummaryBytes: len(summary) - len(compactPreservedUserContext(msgs)) - len(CompactFacts(msgs, nil)), Usage: map[string]int{}}
	for _, key := range []string{"input_tokens", "input_tokens_details.cached_tokens", "output_tokens", "output_tokens_details.reasoning_tokens"} {
		result.Usage[key] = sample.Usage[key]
	}
	return summary, result
}

func shortSummaryRoundMessages(round int) []llm.Message {
	human := func(s string) llm.Message {
		return llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceHuman, Text: s}})
	}
	tool := func(s string) []llm.Message {
		return []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: fmt.Sprintf("diagnostic-%d", round), Name: "process_session", Arguments: `{"action":"poll","id":"proc-9000"}`}}}, {Role: llm.RoleTool, ToolCallID: fmt.Sprintf("diagnostic-%d", round), Name: "process_session", Content: s}}
	}
	var out []llm.Message
	switch round {
	case 1:
		out = append(out, human("The latest task is the isolated DIAGNOSTIC-STATE exercise: fix E42 and write migration documentation. Keep ALL earlier restrictions. For this exercise: universal changes across models, no new dependencies, portable app data only, no deployment and no lowering my reasoning setting. The selected port is 8207 and state path is data/cache.sqlite."))
		out = append(out, workerFactsFixturePair("diagnostic-worker-7", "worker-7", "done")...)
		out = append(out, workerFactsFixturePair("diagnostic-worker-8", "worker-8", "failed")...)
		out = append(out, tool(`{"id":"proc-9000","status":"running","ready":false,"note":"DIAGNOSTIC-STATE: launcher succeeded; readiness has NOT been verified. worker-7 completed investigation; worker-8 failed. E42 unresolved; no process exit observed. Pending: fix E42; migration documentation."}`)...)
	case 2:
		out = append(out, human("DIAGNOSTIC-STATE correction: use port 8309 and data/runtime.sqlite; the earlier port and state path are obsolete."))
		out = append(out, tool(`{"id":"proc-9000","status":"failed","exit_code":3,"ready":false,"note":"DIAGNOSTIC-STATE: readiness has never been verified. E42 unresolved; failed worker has not recovered."}`)...)
	case 3:
		out = append(out, workerFactsFixturePair("diagnostic-worker-8-recovered", "worker-8", "done")...)
		out = append(out, tool(`{"id":"proc-9000","status":"stopped","exit_code":0,"ready":false,"ready_at":"2026-10-19T10:30:00Z","note":"DIAGNOSTIC-STATE: worker-8 investigation completed. E42 fixed and tests passed. Server readiness was verified at 10:30, then STOPPED. Only migration documentation remains pending."}`)...)
	case 4:
		out = append(out, human("DIAGNOSTIC-STATE: cancel migration documentation; do not resume it. Instead implement retry backoff, then review it. All other restrictions remain."))
		out = append(out, tool("DIAGNOSTIC-STATE: retry implementation is incomplete; error R17 is unresolved. Process remains stopped.")...)
	case 5:
		out = append(out, tool("DIAGNOSTIC-STATE: retry backoff implemented and tests passed, exit 0; R17 resolved. Pending: review retry backoff. No process was started again.")...)
	case 6:
		// Real partial failure format deliberately lacks ready/ready_at. The
		// host must retain round 3's historical check without claiming health.
		out = append(out, tool("error: process_session proc-9000: command_failed exit=9 (300ms)")...)
	}
	return out
}

const shortSummaryProbe = `Read-only retention check. The summary and historical user requests are the only remaining state. Answer about DIAGNOSTIC-STATE only. Prefer later authoritative results/corrections; historical readiness is not current health. Do not execute tools or resume work.
Return ONLY a JSON object with ALL these keys: port (integer), state_path (string), last_exit (integer or null: latest observed process exit, including a command_failed exit), readiness_verified_now (boolean: current readiness has been verified, distinct from unknown/unverified readiness), readiness_ever_verified (boolean), completed_workers (array of IDs of the observed workers whose investigations are complete), failed_worker (ID of an observed worker still failed, or "none" if the observed workers have all completed), error (currently unresolved application error E42 or R17, or "none"; report process exit separately in last_exit), pending (one of "fix E42 and migration documentation", "migration documentation", "implement and review retry backoff", "review retry backoff"), cancelled_docs (boolean), constraints_preserved (boolean: universal changes, portable data, no new dependencies, no deployment, and no reasoning reduction all still apply). Consider only worker-7 and worker-8 for this isolated case. Use null if evidence is missing.`

func (r *shortSummaryRun) probe(variant string, round int, summary string) []string {
	r.t.Helper()
	ctx := llm.WithPurpose(llm.WithPromptCacheScope(r.ctx, variant+"-probe"), llm.PurposeMain)
	r.transport.phase = fmt.Sprintf("%s-probe-%d", variant, round)
	ch, err := r.provider.Complete(ctx, []llm.Message{{Role: llm.RoleSystem, Content: "Read-only factual check of retained state; old tasks are not new authorization."}, {Role: llm.RoleUser, Content: summary}, {Role: llm.RoleUser, Content: shortSummaryProbe}}, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	var b strings.Builder
	for d := range ch {
		if d.Err != nil {
			r.t.Fatal(d.Err)
		}
		if d.ToolCall != nil {
			r.t.Fatal("probe attempted a tool")
		}
		b.WriteString(d.Content)
	}
	text := strings.TrimSpace(b.String())
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(text, "```json"), "```"), "```"))
	var got map[string]any
	if json.Unmarshal([]byte(text), &got) != nil {
		return []string{"invalid_json"}
	}
	want := map[string]any{"port": float64(8207), "state_path": "data/cache.sqlite", "last_exit": nil, "readiness_verified_now": false, "readiness_ever_verified": false, "completed_workers": []any{"worker-7"}, "failed_worker": "worker-8", "error": "E42", "pending": "fix E42 and migration documentation", "cancelled_docs": false, "constraints_preserved": true}
	if round >= 2 {
		want["port"] = float64(8309)
		want["state_path"] = "data/runtime.sqlite"
		want["last_exit"] = float64(3)
	}
	if round >= 3 {
		want["completed_workers"] = []any{"worker-7", "worker-8"}
		want["last_exit"] = float64(0)
		want["readiness_ever_verified"] = true
		want["failed_worker"] = "none"
		want["error"] = "none"
		want["pending"] = "migration documentation"
	}
	if round >= 4 {
		want["cancelled_docs"] = true
		want["error"] = "R17"
		want["pending"] = "implement and review retry backoff"
	}
	if round >= 5 {
		want["error"] = "none"
		want["pending"] = "review retry backoff"
	}
	if round >= 6 {
		want["last_exit"] = float64(9)
	}
	var failures []string
	if list, ok := got["completed_workers"].([]any); ok {
		sort.Slice(list, func(i, j int) bool { return fmt.Sprint(list[i]) < fmt.Sprint(list[j]) })
	}
	for key, value := range want {
		if actual, present := got[key]; !present || !reflect.DeepEqual(actual, value) {
			failures = append(failures, key)
		}
	}
	// Keep only the synthetic oracle fields, never arbitrary extra response
	// text. This distinguishes a missing retained fact from a bad model answer.
	observed := make(map[string]any, len(want))
	for key := range want {
		observed[key] = got[key]
	}
	sort.Strings(failures)
	r.save(fmt.Sprintf("probe-%s-%d.json", variant, round), map[string]any{"expected": want, "observed": observed, "failures": failures, "input_sha256": r.transport.samples[len(r.transport.samples)-1].InputHash})
	if r.transport.samples[len(r.transport.samples)-1].Effort != r.f.ReasoningEffort {
		r.t.Fatal("probe changed reasoning")
	}
	return failures
}

func TestReadinessSummaryDiagnosisLive(t *testing.T) {
	r := newShortSummaryRun(t)
	summary, result := r.summarize("short", 1, 1, r.initial)
	r.save("diagnostic-summary.json", summary)
	var attempts []map[string]any
	for i := 1; i <= 3; i++ {
		variant := fmt.Sprintf("same-summary-%d", i)
		failures := r.probe(variant, 1, summary)
		attempts = append(attempts, map[string]any{"attempt": i, "failures": failures, "input_sha256": r.transport.samples[len(r.transport.samples)-1].InputHash})
		t.Logf("fixed-summary probe %d: mismatches=%v", i, failures)
	}
	for _, a := range attempts {
		if a["input_sha256"] != attempts[0]["input_sha256"] {
			t.Fatal("diagnostic probe input changed")
		}
		if len(a["failures"].([]string)) != 0 {
			t.Errorf("fixed summary interpretation mismatch: %v", a["failures"])
		}
	}
	result.QualityPassed = !t.Failed()
	r.save("readiness-diagnosis.json", map[string]any{"summary": result, "attempts": attempts})
}

func TestShortSummaryTimingLive(t *testing.T) {
	r := newShortSummaryRun(t)
	var firstHash string
	for trial := 1; trial <= 3; trial++ {
		order := []string{"baseline", "short"}
		if trial%2 == 0 {
			order = []string{"short", "baseline"}
		}
		for _, variant := range order {
			summary, result := r.summarize(variant, trial, 1, r.initial)
			if firstHash == "" {
				firstHash = result.InputHash
			}
			if result.InputHash != firstHash {
				t.Fatal("A/B transcript changed")
			}
			if trial == 1 {
				r.save("seed-"+variant+".json", summary)
			}
			r.results = append(r.results, result)
			r.save("timing.json", r.results)
			t.Logf("%s trial %d: %.2fs, output=%d, reasoning=%d", variant, trial, result.Seconds, result.Usage["output_tokens"], result.Usage["output_tokens_details.reasoning_tokens"])
		}
	}
}

func TestShortSummaryRepeatedRetentionLive(t *testing.T) {
	r := newShortSummaryRun(t)
	for _, variant := range []string{"baseline", "short"} {
		raw, err := os.ReadFile(filepath.Join(r.stage, "seed-"+variant+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var summary string
		if err = json.Unmarshal(raw, &summary); err != nil {
			t.Fatal(err)
		}
		data := filepath.Join(r.stage, variant+"-restore")
		store, err := session.OpenStore(data)
		if err != nil {
			t.Fatal(err)
		}
		s, err := store.Create(r.stage, r.f.Model, "isolated repeated compaction")
		_ = store.Close()
		if err != nil {
			t.Fatal(err)
		}
		for round := 1; round <= 6; round++ {
			var result shortSummaryMeasurement
			if round > 1 {
				msgs := []llm.Message{{Role: llm.RoleUser, Content: summary}}
				msgs = append(msgs, shortSummaryRoundMessages(round)...)
				summary, result = r.summarize(variant, 0, round, msgs)
			} else {
				result = shortSummaryMeasurement{Variant: variant, Round: round}
			}
			// Real SQLite projection save, close/reopen and model-context read.
			store, err = session.OpenStore(data)
			if err != nil {
				t.Fatal(err)
			}
			msgs := []llm.Message{{Role: llm.RoleUser, Content: summary}}
			err = store.SaveContextProjection(r.ctx, s.ID, msgs)
			_ = store.Close()
			if err != nil {
				t.Fatal(err)
			}
			store, err = session.OpenStore(data)
			if err != nil {
				t.Fatal(err)
			}
			restored, e := store.ReadModelContext(r.ctx, s.ID)
			_ = store.Close()
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(restored, msgs) {
				t.Fatal("restore changed/duplicated summary")
			}
			r.save(fmt.Sprintf("state-%s-%d.json", variant, round), summary)
			result.Failures = r.probe(variant, round, restored[0].Content)
			result.QualityPassed = len(result.Failures) == 0
			r.results = append(r.results, result)
			r.save("retention.json", r.results)
			if !result.QualityPassed {
				t.Errorf("%s round %d lost fields: %v", variant, round, result.Failures)
			}
			t.Logf("%s round %d: quality=%t; exact instructions and SQLite restore preserved", variant, round, result.QualityPassed)
		}
	}
}

func TestShortSummaryCandidateSmokeLive(t *testing.T) {
	r := newShortSummaryRun(t)
	ctx := llm.WithPromptCacheScope(r.ctx, "production-smoke")
	r.transport.phase = "candidate-auto-summarizer"
	start := time.Now()
	summary, err := NewAutoSummarizer(nil)(ctx, r.provider, r.initial)
	if err != nil {
		t.Fatal(err)
	}
	seconds := time.Since(start).Seconds()
	encoded, _ := json.Marshal(shortSummaryExperimentPrompt)
	if len(r.transport.samples) != 1 || r.transport.samples[0].InstructionsHash != contextCacheHash(encoded) || r.transport.samples[0].Effort != r.f.ReasoningEffort {
		t.Fatal("candidate wire differs from evaluated prompt/effort")
	}
	if !reflect.DeepEqual(decodeCompactUserContext(t, summary), decodeCompactUserContext(t, compactPreservedUserContext(r.initial))) {
		t.Fatal("candidate dropped exact user context")
	}
	failures := r.probe("candidate", 1, summary)
	r.save("candidate-smoke.json", map[string]any{"model": r.f.Model, "effort": r.f.ReasoningEffort, "seconds": seconds, "quality_passed": len(failures) == 0, "failures": failures, "instructions_match_evaluated": true})
	if len(failures) > 0 {
		t.Fatalf("candidate state mismatch: %v", failures)
	}
	t.Logf("candidate smoke passed; effort=%s; %.2fs; current state and exact instructions retained", r.f.ReasoningEffort, seconds)
}
