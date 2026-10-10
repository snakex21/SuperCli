package webgui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func TestWebContextRefreshKeepsWorkerEndpointSeparate(t *testing.T) {
	var mainReads, workerReads atomic.Int32
	metadataServer := func(reads *atomic.Int32, window int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/api/v1/models" {
				t.Errorf("unexpected request=%s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
				return
			}
			reads.Add(1)
			fmt.Fprintf(w, `{"data":[{"id":"shared-model","loaded_context_length":%d}]}`, window)
		}))
	}
	mainServer := metadataServer(&mainReads, 4096)
	defer mainServer.Close()
	workerServer := metadataServer(&workerReads, 8192)
	defer workerServer.Close()
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	writeDataConfig(t, dir, fmt.Sprintf(`task_model = "worker-profile/shared-model"
[[providers]]
name = "worker-profile"
type = "openai"
base_url = %q
api_key = "worker-key"
model = "shared-model"
`, workerServer.URL+"/v1"))
	eng.cfg = config.Config{Provider: config.ProviderOpenAI, BaseURL: mainServer.URL + "/v1", APIKey: "main-key", Model: "shared-model"}
	if err := eng.refreshLocalContextWindows(context.Background()); err != nil {
		t.Fatal(err)
	}
	if mainReads.Load() != 1 || workerReads.Load() != 1 {
		t.Fatalf("native metadata reads main=%d worker=%d", mainReads.Load(), workerReads.Load())
	}
	if main := llm.ProviderRuntimeContext(mainServer.URL+"/v1", "main-key", "shared-model"); main != 4096 {
		t.Fatalf("main runtime window=%d", main)
	}
	if worker := llm.ProviderRuntimeContext(workerServer.URL+"/v1", "worker-key", "shared-model"); worker != 8192 {
		t.Fatalf("worker borrowed another endpoint's window=%d", worker)
	}
}

func TestWebSwitchModelContextCanceledBeforeSelectionCommit(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	previous := eng.ModelName()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := eng.SwitchModelContext(ctx, "another-echo", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled model switch=%v", err)
	}
	if got := eng.ModelName(); got != previous {
		t.Fatalf("canceled model switch changed %q to %q", previous, got)
	}
	if model, _ := LastModel(dir); model != "" {
		t.Fatalf("canceled model switch persisted %q", model)
	}
}
