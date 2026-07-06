package app

// Live model-per-task test against two real llama.cpp servers. Skipped
// unless all four env vars are set:
//
//	SUPERCLI_LIVE_BASEURL        orchestrator, e.g. http://127.0.0.1:8089/v1
//	SUPERCLI_LIVE_MODEL          e.g. qwen3.5-9b
//	SUPERCLI_LIVE_TASK_BASEURL   worker host,  e.g. http://127.0.0.1:8091/v1
//	SUPERCLI_LIVE_TASK_MODEL     e.g. ministral-3b
//
// Both backends sit behind in-test counting reverse proxies, so the
// test can prove where each chat completion actually landed:
//
//   - override (task_model set): the coordinator's turns hit host 1,
//     the delegated worker's turns hit host 2, the worker's report
//     returns to the coordinator, and the task summary carries the
//     model= telemetry suffix;
//   - default (no task_model): every request hits host 1 and the
//     summary keeps its historical single-model format.
//
// The worker wiring goes through the same production pieces main.go
// uses: resolveTaskWorkerConfig → buildProvider → AgentTool.WorkerProvider
// (+ the lazy /v1/models WorkerPing).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/system/config"
	"supercli/internal/tools"
)

// countingProxy fronts an OpenAI-compat backend and counts the chat
// completion requests that pass through it.
func countingProxy(t *testing.T, backendBase string, chatHits *atomic.Int32) *httptest.Server {
	t.Helper()
	u, err := url.Parse(backendBase)
	if err != nil {
		t.Fatalf("parse backend %q: %v", backendBase, err)
	}
	rp := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: u.Scheme, Host: u.Host})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			chatHits.Add(1)
		}
		rp.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runDelegatedTask drives one coordinator session whose only tool is
// `task`, so the model must delegate. Returns the parent transcript
// (joined message contents) for inspection.
func runDelegatedTask(t *testing.T, orch llm.Provider, worker llm.Provider, ping func(context.Context) error) string {
	t.Helper()
	base := tools.NewRegistry()
	subReg := agent.NewSubAgentRegistry()
	agent.MustRegisterAll(subReg, agent.BuiltinSubAgents())

	loop, err := agent.NewLoop(agent.LoopConfig{
		Provider: orch,
		Registry: base,
		System: "You are a coordinator. You cannot answer questions yourself. " +
			"For ANY user request you MUST call the task tool to delegate it to a worker, " +
			"then relay the worker's report in one short sentence.",
		MaxSteps: 4,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	at, err := agent.NewAgentTool(subReg, loop, base, orch, nil,
		func(cfg agent.LoopConfig) (*agent.Loop, error) { return agent.NewLoop(cfg) })
	if err != nil {
		t.Fatalf("NewAgentTool: %v", err)
	}
	at.WorkerProvider = worker
	at.WorkerPing = ping
	base.MustRegister(at.Spec())
	base.MarkAlwaysOn("task") // same as main.go's coordinator wiring

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	ch, err := loop.Run(ctx, "Ask a worker: what is the capital of France? Reply with the worker's answer.")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for ev := range ch {
		if e, ok := ev.(agent.ErrorEvent); ok {
			t.Fatalf("loop error: %v", e.Err)
		}
	}
	var b strings.Builder
	for _, m := range loop.AllMessages() {
		b.WriteString(string(m.Role) + ": " + m.Content + "\n")
	}
	return b.String()
}

func TestTaskModel_TwoHosts_Live(t *testing.T) {
	orchURL := os.Getenv("SUPERCLI_LIVE_BASEURL")
	orchModel := os.Getenv("SUPERCLI_LIVE_MODEL")
	taskURL := os.Getenv("SUPERCLI_LIVE_TASK_BASEURL")
	taskModel := os.Getenv("SUPERCLI_LIVE_TASK_MODEL")
	if orchURL == "" || orchModel == "" || taskURL == "" || taskModel == "" {
		t.Skip("live test: set SUPERCLI_LIVE_BASEURL/_MODEL and SUPERCLI_LIVE_TASK_BASEURL/_MODEL")
	}
	llm.SetThinkingEnabled(false)
	t.Cleanup(func() { llm.SetThinkingEnabled(true) })

	var orchHits, workerHits atomic.Int32
	orchProxy := countingProxy(t, orchURL, &orchHits)
	workerProxy := countingProxy(t, taskURL, &workerHits)

	cfg := config.Config{Provider: "openai", BaseURL: orchProxy.URL + "/v1", Model: orchModel}
	orch, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: cfg.BaseURL, Model: cfg.Model})
	if err != nil {
		t.Fatalf("orchestrator provider: %v", err)
	}

	// --- override: task_model = "small/<worker model>" -----------------
	tomlCfg := config.TomlConfig{
		TaskModel: "small/" + taskModel,
		Providers: []config.ProviderConf{
			{Name: "small", Type: "openai", BaseURL: workerProxy.URL + "/v1"},
		},
	}
	workerCfg, ok := resolveTaskWorkerConfig(tomlCfg, cfg)
	if !ok {
		t.Fatal("resolveTaskWorkerConfig: expected an override")
	}
	workerProv, err := buildProvider(workerCfg, t.TempDir(), llm.NewCapabilityRegistry())
	if err != nil {
		t.Fatalf("worker provider: %v", err)
	}
	ping := func(pctx context.Context) error {
		_, pingErr := llm.ListProviderModelContexts(pctx, workerCfg.BaseURL, workerCfg.APIKey)
		return pingErr
	}

	transcript := runDelegatedTask(t, orch, workerProv, ping)
	oc, wc := orchHits.Load(), workerHits.Load()
	t.Logf("override: orchestrator chat hits=%d, worker chat hits=%d", oc, wc)
	if wc < 1 {
		t.Errorf("worker host got %d chat completions — delegation did not switch hosts", wc)
	}
	if oc < 2 {
		t.Errorf("orchestrator host got %d chat completions — expected the coordinator turns there", oc)
	}
	if !strings.Contains(transcript, "<task-notification>") || !strings.Contains(transcript, "<status>done</status>") {
		t.Errorf("no successful task notification in parent transcript:\n%s", transcript)
	}
	if !strings.Contains(transcript, "model="+taskModel) {
		t.Errorf("summary missing model=%s telemetry:\n%s", taskModel, transcript)
	}
	if !strings.Contains(strings.ToLower(transcript), "paris") {
		t.Errorf("worker's answer did not reach the coordinator:\n%s", transcript)
	}

	// --- default: no task_model → single host --------------------------
	orchHits.Store(0)
	workerHits.Store(0)
	transcript = runDelegatedTask(t, orch, nil, nil)
	oc, wc = orchHits.Load(), workerHits.Load()
	t.Logf("default:  orchestrator chat hits=%d, worker chat hits=%d", oc, wc)
	if wc != 0 {
		t.Errorf("default run leaked %d chat completions to the worker host", wc)
	}
	if !strings.Contains(transcript, "<task-notification>") {
		t.Errorf("no task notification in default transcript:\n%s", transcript)
	}
	if strings.Contains(transcript, "model=") {
		t.Errorf("default summary must not carry a model suffix:\n%s", transcript)
	}
}
