package webgui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image/gif"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

// Opt-in, real GUI conversation: each HTTP turn reconstructs a fresh Loop from
// the persisted session. No fixture URL, downloaded asset, or fake tool result
// is supplied to the model. Ordinary tests never contact a model or the web.
type downloadLiveRequest struct {
	Tools             []string `json:"tools"`
	ToolSHA256        []string `json:"tool_sha256,omitempty"`
	DefinitionsSHA256 string   `json:"definitions_sha256,omitempty"`
	MessageBytes      int      `json:"message_bytes"`
}

type downloadLiveProvider struct {
	inner    llm.Provider
	mu       sync.Mutex
	requests []downloadLiveRequest
}

func (p *downloadLiveProvider) Name() string         { return p.inner.Name() }
func (p *downloadLiveProvider) Unwrap() llm.Provider { return p.inner }
func (p *downloadLiveProvider) Complete(ctx context.Context, messages []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	if trace, _ := ctx.Value(chatPhaseProbeTraceKey{}).(*chatPhaseProbeTrace); trace != nil {
		trace.mark("provider_entry")
	}
	r := downloadLiveRequest{}
	for _, d := range defs {
		r.Tools = append(r.Tools, d.Name)
		b, err := json.Marshal(d)
		if err == nil {
			digest := sha256.Sum256(b)
			r.ToolSHA256 = append(r.ToolSHA256, hex.EncodeToString(digest[:]))
		}
	}
	if b, err := json.Marshal(defs); err == nil {
		digest := sha256.Sum256(b)
		r.DefinitionsSHA256 = hex.EncodeToString(digest[:])
	}
	if b, err := json.Marshal(messages); err == nil {
		r.MessageBytes = len(b)
	}
	p.mu.Lock()
	p.requests = append(p.requests, r)
	p.mu.Unlock()
	return p.inner.Complete(ctx, messages, defs)
}

type downloadLiveFile struct {
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
	Frames int    `json:"frames"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type downloadLiveTurn struct {
	Prompt            string               `json:"prompt"`
	WallMS            int64                `json:"wall_ms"`
	Summary           session.TurnSummary  `json:"summary"`
	Trace             chatPhaseProbeResult `json:"trace"`
	Messages          []llm.Message        `json:"messages"`
	Files             []downloadLiveFile   `json:"files"`
	FinalText         string               `json:"final_text"`
	Passed            bool                 `json:"passed"`
	AfterSaveTools    int                  `json:"tool_calls_after_last_save"`
	AfterSaveRequests int                  `json:"main_requests_after_last_save"`
	SaveToReplyMS     int64                `json:"last_save_to_final_ms"`
	ReusedResults     int                  `json:"reused_results"`
}

func TestDownloadFollowupLiveGUI(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("SUPERCLI_DOWNLOAD_LIVE_URL"))
	if baseURL == "" {
		t.Skip("opt-in local-model download conversation")
	}
	u, err := url.Parse(baseURL)
	model := strings.TrimSpace(os.Getenv("SUPERCLI_DOWNLOAD_LIVE_MODEL"))
	if err != nil || u.User != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") || u.RawQuery != "" || u.Fragment != "" || model == "" {
		t.Fatal("requires explicit loopback model URL and model name")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(cwd, "..", ".."))
	parent := filepath.Join(root, ".tmp", "gif-followup-live")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	run, err := os.MkdirTemp(parent, "run-")
	if err != nil {
		t.Fatal(err)
	}
	home, data, export := filepath.Join(run, "workspace"), filepath.Join(run, "data"), filepath.Join(run, "exports")
	downloads := filepath.Join(run, "Pobrane")
	for _, path := range []string{home, data, export, downloads} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// Keep normal adaptive delegation and automatic navigation/preflight. The
	// application must itself recognize a self-contained public download task.
	if err := os.WriteFile(filepath.Join(data, "config.toml"), []byte("thinking = true\nreasoning_effort = \"max\"\nmax_steps = 12\npreflight_repo = true\nlanguage = \"pl\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	previousThinking, previousEffort := llm.ThinkingEnabled(), llm.ReasoningEffort()
	llm.SetThinkingEnabled(true)
	if err := llm.SetReasoningEffort("max"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { llm.SetThinkingEnabled(previousThinking); _ = llm.SetReasoningEffort(previousEffort) })
	cfg := echoConfig()
	cfg.Provider, cfg.BaseURL, cfg.Model = config.ProviderOpenAI, baseURL, model
	cfg.APIKey, cfg.Stream, cfg.MaxTokens, cfg.Timeout = "", true, 8192, 180*time.Second
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(cfg, home, data)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	provider := &downloadLiveProvider{inner: eng.prov}
	eng.prov = provider
	handler := NewServer(eng, false).Handler()
	report := struct {
		Model     string                `json:"model"`
		SessionID string                `json:"session_id"`
		Run       string                `json:"run"`
		Thinking  bool                  `json:"thinking"`
		Turns     []downloadLiveTurn    `json:"turns"`
		Requests  []downloadLiveRequest `json:"requests"`
		Passed    bool                  `json:"passed"`
	}{Model: model, Run: run, Thinking: true}
	defer func() {
		provider.mu.Lock()
		report.Requests = append([]downloadLiveRequest(nil), provider.requests...)
		provider.mu.Unlock()
		report.Passed = !t.Failed() && len(report.Turns) == 3
		b, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(run, "receipt.json"), b, 0600)
		}
		if err != nil {
			t.Errorf("save live receipt: %v", err)
		}
		t.Logf("live receipt: %s", filepath.Join(run, "receipt.json"))
	}()
	firstName := "supercli-followup-" + filepath.Base(run) + ".gif"
	second := filepath.Join(home, "gify")
	prompts := []string{
		"Cześć pobierz mi jakiś fajny gif z Tenora do folderu Pobrane tutaj: " + downloads + ". Zapisz jako " + firstName + ". Nie nadpisuj istniejących plików.",
		"dzięki a możesz pobrać kolejny gif ale dać go tutaj? " + second,
		"A teraz jeszcze dwa różne gify z kotami tutaj: " + export,
	}
	expected := []string{filepath.Join(downloads, firstName), second, export}
	counts := []int{1, 1, 2}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	lastSeq := -1
	for i, prompt := range prompts {
		trace, sid := chatPhaseProbeTurn(t, handler, ctx, prompt, report.SessionID, "download-live-"+filepath.Base(run)+string(rune('1'+i)), false, true)
		report.SessionID = sid
		turn := downloadLiveTurn{Prompt: prompt, WallMS: trace.point("receipt_return") / int64(time.Millisecond), Trace: trace.result("local-qwen-gui", "download-followup")}
		var lastSave int64
		for _, point := range turn.Trace.Timeline {
			if point.Name == "successful_download" {
				lastSave = point.NS
			}
		}
		if lastSave > 0 {
			turn.SaveToReplyMS = (trace.point("receipt_return") - lastSave) / int64(time.Millisecond)
			for _, point := range turn.Trace.Timeline {
				if point.NS <= lastSave {
					continue
				}
				if point.Name == "provider_entry" {
					turn.AfterSaveRequests++
				}
				if point.Name == "sse_tool_call" {
					turn.AfterSaveTools++
				}
			}
		}
		store, err := eng.sessionStore()
		if err != nil {
			t.Fatal(err)
		}
		summaries, err := store.ReadTurnSummaries(ctx, sid)
		if err != nil || len(summaries) == 0 {
			t.Fatalf("turn summary missing: %v", err)
		}
		turn.Summary = summaries[len(summaries)-1]
		encoded, err := store.ReadMessages(ctx, sid)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range encoded {
			if row.Seq <= lastSeq {
				continue
			}
			msg, err := row.ToMessage()
			if err != nil {
				t.Fatal(err)
			}
			turn.Messages = append(turn.Messages, msg)
			if msg.Role == llm.RoleTool && strings.HasPrefix(msg.Content, "[reuse]") {
				turn.ReusedResults++
			}
		}
		if len(encoded) > 0 {
			lastSeq = encoded[len(encoded)-1].Seq
		}
		paths := []string{expected[i]}
		if i > 0 {
			paths = nil
			entries, err := os.ReadDir(expected[i])
			if err == nil {
				for _, entry := range entries {
					if !entry.IsDir() {
						paths = append(paths, filepath.Join(expected[i], entry.Name()))
					}
				}
			}
		}
		for _, path := range paths {
			b, err := os.ReadFile(path)
			if err != nil || len(b) > 20<<20 {
				continue
			}
			g, err := gif.DecodeAll(bytes.NewReader(b))
			if err != nil {
				continue
			}
			sum := sha256.Sum256(b)
			turn.Files = append(turn.Files, downloadLiveFile{Path: path, Bytes: len(b), SHA256: hex.EncodeToString(sum[:]), Frames: len(g.Image), Width: g.Config.Width, Height: g.Config.Height})
		}
		if len(turn.Messages) > 0 {
			last := turn.Messages[len(turn.Messages)-1]
			if last.Role == llm.RoleAssistant {
				turn.FinalText = strings.TrimSpace(regexp.MustCompile(`(?s)<(?:thinking|think)>.*?</(?:thinking|think)>`).ReplaceAllString(last.TextOnly().Content, ""))
			}
		}
		// A guessed page may legitimately return 404 and be recovered. Success
		// requires the requested files and a completed assistant answer;
		// a safety-ceiling error or reasoning-only ending cannot pass.
		turn.Passed = len(turn.Files) == counts[i] && turn.Summary.ToolDiag.Terminal == "" && turn.FinalText != ""
		if i == 2 && len(turn.Files) == 2 && turn.Files[0].SHA256 == turn.Files[1].SHA256 {
			turn.Passed = false
		}
		report.Turns = append(report.Turns, turn)
		t.Logf("turn %d: %.3fs, %d model calls, %d tools, %d failed, %d valid GIFs", i+1, float64(turn.WallMS)/1000, turn.Summary.ModelCalls, turn.Summary.ToolCalls, turn.Summary.ToolFailures, len(turn.Files))
		if !turn.Passed {
			t.Fatalf("turn %d failed; transcript in durable receipt", i+1)
		}
	}
}
