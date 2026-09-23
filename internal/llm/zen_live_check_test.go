package llm

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestZenLiveChatCompletion drives the real OpenAI provider against
// production /chat/completions with the free-tier gate (nested bash+read
// tools, wire session). Live probe 2026-09-23: the body gate is tools-based
// (both bash and read, nested function shape, stream:true) — any
// openai-compatible free model opens the same way. SuperCli injects that
// pair in Complete via ensureOpenCodeZenGateToolDefs; this test pins it.
func TestZenLiveChatCompletion(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if !strings.EqualFold(envOr("ZEN_LIVE", ""), "1") {
		t.Skip("set ZEN_LIVE=1 to run live Zen smoke")
	}

	p, err := NewOpenAI(OpenAIConfig{
		BaseURL:   "https://opencode.ai/zen/v1",
		APIKey:    "public",
		Model:     envOr("ZEN_MODEL", "mimo-v2.6-flash-free"),
		MaxTokens: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithOpenCodeSession(context.Background(), "ses_0123456789abcdef0123456789")
	// SuperCli's real tools — no bash/read names. Complete must inject the gate pair.
	tools := []ToolDef{
		{Name: "web_lookup", Description: "web", Schema: `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`},
		{Name: "tool_search", Description: "search", Schema: `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`},
		{Name: "recall", Description: "memory", Schema: `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`},
	}
	ch, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "Reply with exactly: OK"}}, tools)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var text strings.Builder
	var gotErr error
	for d := range ch {
		if d.Err != nil {
			gotErr = d.Err
		}
		text.WriteString(d.Content)
	}
	if gotErr != nil {
		t.Fatalf("live Zen chat: %v", gotErr)
	}
	t.Logf("text=%q", text.String())
	if text.Len() == 0 {
		t.Fatal("empty completion")
	}
}

// TestZenLiveModels is a lighter live check: /models with impersonated headers.
func TestZenLiveModels(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if !strings.EqualFold(envOr("ZEN_LIVE", ""), "1") {
		t.Skip("set ZEN_LIVE=1 to run live Zen smoke")
	}

	base := "https://opencode.ai/zen/v1"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	ApplyOpenCodeZenHeaders(req, base)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	t.Logf("status=%d bodyLen=%d", resp.StatusCode, len(raw))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("live Zen models status=%d body=%s", resp.StatusCode, string(raw))
	}
}

// TestZenLiveResponsesMuse drives the real NewResponses provider (the Zen
// body dialect from prepareOpenCodeZenResponsesRequest) at production
// /zen/v1/responses with muse-spark-1.3-contributor-free.
func TestZenLiveResponsesMuse(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if !strings.EqualFold(envOr("ZEN_LIVE", ""), "1") {
		t.Skip("set ZEN_LIVE=1 to run live Zen smoke")
	}

	p, err := NewResponses(ResponsesConfig{
		BaseURL: "https://opencode.ai/zen/v1",
		Model:   envOr("ZEN_MODEL", "muse-spark-1.3-contributor-free"),
		Timeout: 60 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithOpenCodeSession(context.Background(), "ses_0123456789abcdef0123456789")
	ch, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "Reply with exactly: CAPTURE_OK"}}, []ToolDef{
		{Name: "bash", Description: "run a command", Schema: `{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var gotErr error
	for d := range ch {
		if d.Err != nil {
			gotErr = d.Err
		}
		text.WriteString(d.Content)
	}
	if gotErr != nil {
		t.Fatalf("live Zen responses: %v", gotErr)
	}
	t.Logf("text=%q", text.String())
	if text.Len() == 0 {
		t.Fatal("empty completion body")
	}
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
