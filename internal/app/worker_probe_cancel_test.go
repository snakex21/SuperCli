package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/config"
	"supercli/internal/tools"
)

func TestWiredWorkerRecoversAfterCanceledHTTPProbe(t *testing.T) {
	entered := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		if requests.Add(1) == 1 {
			close(entered)
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"worker","context_length":32768}]}`))
	}))
	defer server.Close()
	parent, _ := llm.NewEcho("coordinator")
	worker, _ := llm.NewEcho("worker")
	reg := tools.NewRegistry()
	off := false
	at, err := wireAgentTool(agentToolWiring{registry: reg, provider: parent, home: t.TempDir(), tomlCfg: config.TomlConfig{PreflightRepo: &off}, taskWorkerProvider: worker, taskWorkerCfg: config.Config{Provider: config.ProviderOpenAI, BaseURL: server.URL + "/v1", Model: "worker"}})
	if err != nil {
		t.Fatal(err)
	}
	task, ok := reg.Get("task")
	if !ok {
		t.Fatal("task was not registered")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	completed := make(chan tools.Result, 1)
	go func() {
		result, err := task.Fn(ctx, json.RawMessage(`{"prompt":"inspect"}`))
		if err != nil {
			result.Err = err
		}
		completed <- result
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("wired probe did not reach the HTTP endpoint")
	}
	cancel()
	if result := <-completed; !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("cancellation lost: %+v", result)
	}
	if len(at.Workers.List()) != 0 {
		t.Error("canceled HTTP probe registered an unused worker")
	}
	for i := 0; i < 2; i++ {
		result, err := task.Fn(context.Background(), json.RawMessage(`{"prompt":"inspect"}`))
		if err != nil || result.Err != nil || !strings.Contains(result.Text, "model=worker") {
			t.Fatalf("next task ignored the selected worker backend: result=%+v err=%v", result, err)
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("HTTP probe count=%d, want canceled attempt plus one cached success", requests.Load())
	}
}
