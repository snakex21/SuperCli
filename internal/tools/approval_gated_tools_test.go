package tools_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"supercli/internal/system/config"
	"supercli/internal/tools/core"
	"supercli/internal/tools/headless"
	"supercli/internal/tools/interactive"
	"supercli/internal/tools/mediagen"
)

// The tools that used the old ConfirmAction prompt (headless_control,
// generate_image/generate_video, host_power) now go through the host approval
// gate: batch runs deny by default with an audit entry, the [approvals]
// allowlist can allow a narrow scope, and the model cannot confirm by itself.

func readAudit(t *testing.T, path string) []interactive.ApprovalAuditEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []interactive.ApprovalAuditEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e interactive.ApprovalAuditEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("audit line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

func batchGate(t *testing.T, allow ...string) (*interactive.ApprovalGate, string) {
	audit := filepath.Join(t.TempDir(), "approvals.jsonl")
	return &interactive.ApprovalGate{AutoApprove: allow, NonInteractiveReason: "batch run", AuditPath: audit}, audit
}

func TestHeadlessMutationDeniedInBatchAndAudited(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"value": nil})
	}))
	defer server.Close()
	data := t.TempDir()
	conf := "[headless.targets.lab]\nprotocol = \"webdriver\"\nendpoint = \"" + server.URL + "\"\nallowed_actions = [\"navigate\", \"status\"]\n"
	if err := os.WriteFile(filepath.Join(data, "config.toml"), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := core.NewRegistry()
	registry.MustRegister(headless.New(t.TempDir(), data).Spec())
	gate, audit := batchGate(t)
	ctx := core.WithApprover(context.Background(), gate)
	raw := json.RawMessage(`{"protocol":"webdriver","endpoint":"` + server.URL + `","action":"navigate","session_id":"s1","url":"https://example.com/"}`)
	res, err := registry.Execute(ctx, "headless_control", raw)
	if err != nil || !errors.Is(res.Err, core.ErrApprovalRequired) || !strings.Contains(res.Err.Error(), "headless_control:navigate:webdriver:lab") {
		t.Fatalf("batch navigate = %v / %v", res.Err, err)
	}
	if requests.Load() != 0 {
		t.Fatal("denied action contacted the driver")
	}
	entries := readAudit(t, audit)
	if len(entries) != 1 || entries[0].Tool != "headless_control" || entries[0].Op != "navigate" || entries[0].Approved || entries[0].Mode != core.ApprovalUnavailable || entries[0].Risk != core.RiskExternal {
		t.Fatalf("audit = %+v", entries)
	}
	// Reads need no approval and are not audited.
	if res, _ := registry.Execute(ctx, "headless_control", json.RawMessage(`{"protocol":"webdriver","endpoint":"`+server.URL+`","action":"status"}`)); res.Err != nil {
		t.Fatalf("status: %v", res.Err)
	}
	if len(readAudit(t, audit)) != 1 {
		t.Fatal("read action was gated")
	}
	// A model-supplied confirmation is not an argument of the tool.
	res, _ = registry.Execute(ctx, "headless_control", json.RawMessage(`{"protocol":"webdriver","endpoint":"`+server.URL+`","action":"navigate","session_id":"s1","url":"https://example.com/","confirm":true}`))
	if res.Err == nil || !errors.Is(res.Err, core.ErrInvalidToolArgs) {
		t.Fatalf("confirm argument accepted: %v", res.Err)
	}
	// Execute reached without the registry never mutates.
	before := requests.Load()
	direct, _ := headless.New(t.TempDir(), data).Execute(interactive.WithAskChannel(context.Background(), make(chan interactive.AskRequest, 1)), raw)
	if direct.Err == nil || !strings.Contains(direct.Err.Error(), "not approved") || requests.Load() != before {
		t.Fatalf("direct Execute = %v (requests=%d)", direct.Err, requests.Load())
	}
}

func TestMediaGenerationBatchDenyAllowlistAndNoSelfConfirm(t *testing.T) {
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAwS2OUAAAAABJRU5ErkJggg==")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString(png)}}})
	}))
	defer server.Close()
	cfg := &config.MediaGenerationProviderConf{Enabled: true, Provider: "openai", BaseURL: server.URL, Model: "local-image"}
	registry := core.NewRegistry()
	registry.MustRegister(mediagen.NewImage(t.TempDir(), cfg).Spec())

	gate, audit := batchGate(t)
	res, _ := registry.Execute(core.WithApprover(context.Background(), gate), "generate_image", json.RawMessage(`{"prompt":"a cat"}`))
	if !errors.Is(res.Err, core.ErrApprovalRequired) || requests.Load() != 0 {
		t.Fatalf("batch generation = %v (requests=%d)", res.Err, requests.Load())
	}
	if e := readAudit(t, audit); len(e) != 1 || e[0].Tool != "generate_image" || e[0].Op != "generate" || e[0].Approved || e[0].Scope != "generate_image:generate:openai:local-image" {
		t.Fatalf("audit = %+v", e)
	}
	// The model cannot confirm on its own.
	res, _ = registry.Execute(core.WithApprover(context.Background(), gate), "generate_image", json.RawMessage(`{"prompt":"a cat","confirm":true}`))
	if !errors.Is(res.Err, core.ErrInvalidToolArgs) || requests.Load() != 0 {
		t.Fatalf("self-confirmation = %v", res.Err)
	}

	allowed, allowedAudit := batchGate(t, "generate_image:generate:openai")
	res, _ = registry.Execute(core.WithApprover(context.Background(), allowed), "generate_image", json.RawMessage(`{"prompt":"a cat"}`))
	if res.Err != nil || requests.Load() != 1 {
		t.Fatalf("allowlisted generation = %v (requests=%d)", res.Err, requests.Load())
	}
	if e := readAudit(t, allowedAudit); len(e) != 1 || !e[0].Approved || e[0].Mode != core.ApprovalAllowlist {
		t.Fatalf("audit = %+v", e)
	}
	// The prompt never reaches the audit log.
	if data, _ := os.ReadFile(allowedAudit); strings.Contains(string(data), "a cat") {
		t.Fatalf("audit log contains the prompt: %s", data)
	}
}

func TestMediaGenerationApprovalCardIsTrusted(t *testing.T) {
	cfg := &config.MediaGenerationProviderConf{Enabled: true, Provider: "openai", BaseURL: "http://127.0.0.1:9", Model: "local-image"}
	registry := core.NewRegistry()
	registry.MustRegister(mediagen.NewImage(t.TempDir(), cfg).Spec())
	ch := make(chan interactive.AskRequest, 1)
	gate := &interactive.ApprovalGate{Language: func() string { return "pl" }}
	ctx := interactive.WithAskChannel(core.WithApprover(context.Background(), gate), ch)
	done := make(chan core.Result, 1)
	go func() {
		res, _ := registry.Execute(ctx, "generate_image", json.RawMessage(`{"prompt":"a cat"}`))
		done <- res
	}()
	req := <-ch
	card := req.Approval
	if card == nil || card.Risk != core.RiskExternal || card.Reversible || card.SimilarLabel != "" || card.SummaryKey != "approval.summary.generate_image" {
		t.Fatalf("card = %+v", card)
	}
	if !strings.Contains(card.Summary, "local-image") || strings.Contains(card.Summary, "Generate one") || req.Header == "Approval required" {
		t.Fatalf("card was not localized: header=%q summary=%q", req.Header, card.Summary)
	}
	req.Respond <- interactive.AskAnswer{Cancelled: true}
	if res := <-done; !errors.Is(res.Err, core.ErrApprovalRequired) {
		t.Fatalf("denied generation = %v", res.Err)
	}
}
