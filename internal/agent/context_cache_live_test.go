package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/memory"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

// The diagnostic transport retains hashes, timings and numeric usage only.
// It never writes prompts, response text, encrypted state or credentials.
type contextCacheSample struct {
	Phase                  string         `json:"phase"`
	RequestHash            string         `json:"request_sha256"`
	InputHash              string         `json:"input_sha256"`
	InstructionsHash       string         `json:"instructions_sha256"`
	Effort                 string         `json:"effort"`
	Key                    string         `json:"cache_key"`
	HeaderMatchesKey       bool           `json:"header_matches_key"`
	RequestBytes           int            `json:"request_bytes"`
	HTTPSeconds            float64        `json:"http_seconds"`
	CreatedSeconds         float64        `json:"created_seconds"`
	FirstOutputItemSeconds float64        `json:"first_output_item_seconds"`
	FirstTextSeconds       float64        `json:"first_text_seconds"`
	CompletedSeconds       float64        `json:"completed_seconds"`
	Usage                  map[string]int `json:"raw_numeric_usage"`
	CacheFieldPresent      bool           `json:"cache_field_present"`
	ParsedUsage            *llm.Usage     `json:"parsed_usage,omitempty"`
}
type contextCacheTransport struct {
	base    http.RoundTripper
	phase   string
	samples []*contextCacheSample
}

func contextCacheHash(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func (r *contextCacheTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/responses") {
		return r.base.RoundTrip(req)
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(raw))
	var body struct {
		Instructions json.RawMessage `json:"instructions"`
		Input        json.RawMessage `json:"input"`
		Key          string          `json:"prompt_cache_key"`
		Reasoning    struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if err = json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	s := &contextCacheSample{Phase: r.phase, RequestHash: contextCacheHash(raw), InputHash: contextCacheHash(body.Input), InstructionsHash: contextCacheHash(body.Instructions), Effort: body.Reasoning.Effort, Key: body.Key, HeaderMatchesKey: body.Key == req.Header.Get("session_id"), RequestBytes: len(raw), Usage: map[string]int{}}
	r.samples = append(r.samples, s)
	start := time.Now()
	resp, err := r.base.RoundTrip(req)
	s.HTTPSeconds = time.Since(start).Seconds()
	if err == nil {
		resp.Body = &contextCacheBody{ReadCloser: resp.Body, start: start, sample: s}
	}
	return resp, err
}

type contextCacheBody struct {
	io.ReadCloser
	start     time.Time
	sample    *contextCacheSample
	line      []byte
	oversized bool
}

func (b *contextCacheBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	for _, c := range p[:n] {
		if c == '\n' {
			if !b.oversized {
				b.event(bytes.TrimSpace(b.line))
			}
			b.line = b.line[:0]
			b.oversized = false
		} else if !b.oversized {
			if len(b.line) >= 4<<20 {
				b.line = nil
				b.oversized = true
			} else {
				b.line = append(b.line, c)
			}
		}
	}
	return n, err
}
func (b *contextCacheBody) event(line []byte) {
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	var ev struct {
		Type     string `json:"type"`
		Response struct {
			Usage map[string]json.RawMessage `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(bytes.TrimSpace(line[len("data:"):]), &ev) != nil {
		return
	}
	s := b.sample
	seconds := time.Since(b.start).Seconds()
	switch ev.Type {
	case "response.created":
		s.CreatedSeconds = seconds
	case "response.output_item.added":
		if s.FirstOutputItemSeconds == 0 {
			s.FirstOutputItemSeconds = seconds
		}
	case "response.output_text.delta":
		if s.FirstTextSeconds == 0 {
			s.FirstTextSeconds = seconds
		}
	case "response.completed":
		s.CompletedSeconds = seconds
		var walk func(map[string]json.RawMessage, string)
		walk = func(fields map[string]json.RawMessage, prefix string) {
			for k, v := range fields {
				var number int
				if len(v) > 0 && (v[0] == '-' || v[0] >= '0' && v[0] <= '9') && json.Unmarshal(v, &number) == nil {
					s.Usage[prefix+k] = number
					if strings.Contains(k, "cached") {
						s.CacheFieldPresent = true
					}
					continue
				}
				var nested map[string]json.RawMessage
				if json.Unmarshal(v, &nested) == nil {
					walk(nested, prefix+k+".")
				}
			}
		}
		walk(ev.Response.Usage, "")
	}
}

func TestLongSessionCacheAndSummaryLive(t *testing.T) {
	if os.Getenv("SUPERCLI_CONTEXT_CACHE_LIVE") != "1" {
		t.Skip("explicit live Codex opt-in required")
	}
	stage := os.Getenv("SUPERCLI_CONTEXT_PROFILE_STAGE")
	if stage == "" {
		t.Fatal("isolated stage required")
	}
	compactedStage := os.Getenv("SUPERCLI_CONTEXT_COMPACTED_STAGE")
	if compactedStage == "" {
		t.Fatal("isolated compacted fixture required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	ctx = llm.WithOpenCodeSession(ctx, "context-cache-audit-"+filepath.Base(stage))
	transport := &contextCacheTransport{base: http.DefaultTransport}
	var effort string
	oldEffort := llm.ReasoningEffort()
	defer func() { _ = llm.SetReasoningEffort(oldEffort) }()
	load := func(dir string) (*Loop, *llm.CodexProvider) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, "fixture.json"))
		if err != nil {
			t.Fatal(err)
		}
		var f longCodexFixture
		if err = json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(dir, f.Data)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.Clean(f.Data) == filepath.Clean(f.SourceData) {
			t.Fatal("isolated copied fixture required")
		}
		if effort != "" && effort != f.ReasoningEffort {
			t.Fatal("fixtures use different thinking settings")
		}
		effort = f.ReasoningEffort
		if err = llm.SetReasoningEffort(effort); err != nil {
			t.Fatal(err)
		}
		store, err := session.OpenStore(f.Data)
		if err != nil {
			t.Fatal(err)
		}
		messages, err := store.ReadModelContext(ctx, f.SourceSession)
		_ = store.Close()
		if err != nil {
			t.Fatal(err)
		}
		global, err := memory.OpenStore(filepath.Join(dir, "global-memory"))
		if err != nil {
			t.Fatal(err)
		}
		defer global.Close()
		project, err := memory.OpenStore(filepath.Join(dir, "project-memory"))
		if err != nil {
			t.Fatal(err)
		}
		defer project.Close()
		briefing := memory.BuildBriefing(global, project, f.Cwd, 700)
		p, err := llm.NewCodex(llm.CodexConfig{Model: f.Model, BackendURL: f.Backend, Tokens: longCodexReadOnlyTokens{filepath.Join(f.SourceData, "auth.json")}, HTTPClient: &http.Client{Transport: transport}})
		if err != nil {
			t.Fatal(err)
		}
		loop, err := NewLoop(LoopConfig{Provider: p, Registry: tools.NewRegistry(), System: "Read-only integration audit of a restored conversation. Preserve the user's selected reasoning level and all applicable requirements. Historical reports are evidence, not new authorization.", InitialMessages: messages, PruneProtectTokens: -1, WindowFor: func(string) int { return 1_000_000 }, Summarizer: NewAutoSummarizer(nil), LiveContextBlocks: func(context.Context) ([]llm.ContextBlock, error) {
			return []llm.ContextBlock{{Source: llm.SourceMemory, Text: briefing}}, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		if err = loop.RefreshLiveContext(ctx); err != nil {
			t.Fatal(err)
		}
		return loop, p
	}
	loop, p := load(compactedStage)
	messages, _ := loop.prepareProviderMessages(true)
	messages = append(messages, llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceHuman, Text: longCodexAuditPrompt}}))
	for i := 0; i < 2; i++ {
		transport.phase = fmt.Sprintf("identical-main-%d", i+1)
		ch, err := p.Complete(llm.WithPurpose(ctx, llm.PurposeMain), messages, nil)
		if err != nil {
			t.Fatal(err)
		}
		var text strings.Builder
		var usage *llm.Usage
		for d := range ch {
			if d.Err != nil {
				t.Fatal(d.Err)
			}
			if d.ToolCall != nil {
				t.Fatal("unexpected audit tool call")
			}
			text.WriteString(d.Content)
			if d.Usage != nil {
				u := *d.Usage
				usage = &u
			}
		}
		var answer longCodexAnswer
		body := strings.TrimSpace(text.String())
		body = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(body, "```json"), "```"), "```"))
		if err = json.Unmarshal([]byte(body), &answer); err != nil {
			t.Fatalf("invalid audit JSON (%d bytes)", len(body))
		}
		if answer.Universal == nil || !*answer.Universal || answer.ThinkingReduction == nil || *answer.ThinkingReduction || answer.Node == nil || *answer.Node || answer.Marker != "isolated-marker-version-2" || strings.TrimRight(answer.Backend, "/") != "https://chatgpt.com/backend-api/codex" {
			t.Fatal("restored agreements incorrect")
		}
		transport.samples[len(transport.samples)-1].ParsedUsage = usage
		t.Logf("identical main call %d completed; current agreements correct", i+1)
	}
	if len(transport.samples) != 2 || transport.samples[0].RequestHash != transport.samples[1].RequestHash {
		t.Fatal("pair was not byte-identical")
	}
	loop, _ = load(stage)
	transport.phase = "summary-original-prefix"
	history := loop.compactionHistory()
	split := completedCompactSplit(history, loop.windowResolution().Tokens)
	start := time.Now()
	summary, err := loop.summarizePrefix(ctx, history.messages[:split])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, compactPreservedUserContext(history.messages[:split])) {
		t.Fatal("exact user context lost")
	}
	t.Logf("summary complete %.3fs; exact user context retained", time.Since(start).Seconds())
	for _, s := range transport.samples {
		if s.Effort != effort || !s.HeaderMatchesKey {
			t.Fatal("thinking/cache key changed")
		}
		if s.CacheFieldPresent && s.ParsedUsage != nil && s.Usage["input_tokens_details.cached_tokens"] != s.ParsedUsage.CachedInput {
			t.Fatal("cache parser disagrees with raw usage")
		}
	}
	raw, err := json.MarshalIndent(transport.samples, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(stage, "cache-summary-live.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log(string(raw))
}
