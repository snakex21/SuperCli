package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestZenLiveChatCompletion documents that free-tier Zen rejects
// /chat/completions outright (403 FreeTierError even with bash+read tools
// and a wire-shaped session — live probe 2026-09-22). SuperCli's production
// path is /responses (TestZenLiveResponsesMuse); this test only records the
// endpoint's status so a future gate change is visible.
func TestZenLiveChatCompletion(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if !strings.EqualFold(envOr("ZEN_LIVE", ""), "1") {
		t.Skip("set ZEN_LIVE=1 to run live Zen smoke")
	}

	base := "https://opencode.ai/zen/v1"
	model := envOr("ZEN_MODEL", "mimo-v2.5-free")

	body, err := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": "Reply with exactly: OK"},
		},
		"max_tokens": 32,
		"stream":     false,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(WithOpenCodeSession(ctx, "ses_0123456789abcdef0123456789"))
	ApplyOpenCodeZenHeaders(req, base)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	t.Logf("status=%d body=%s", resp.StatusCode, string(raw))

	switch {
	case resp.StatusCode == http.StatusForbidden:
		// Expected on free tier: only /responses is open (bash+read + session).
		t.Log("chat/completions still FreeTierError — production uses /responses")
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		t.Log("chat/completions unexpectedly open — gate may have changed")
	default:
		t.Fatalf("live Zen chat unexpected status=%d body=%s", resp.StatusCode, string(raw))
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
