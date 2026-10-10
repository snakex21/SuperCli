package config

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
)

func TestRefreshLocalContextWindowsDeduplicatesEndpointAndIsolatesCredentials(t *testing.T) {
	var mu sync.Mutex
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/models" {
			t.Errorf("metadata request=%s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		key := r.Header.Get("Authorization")
		mu.Lock()
		requests[key]++
		mu.Unlock()
		window := 4096
		if key == "Bearer second-key" {
			window = 8192
		}
		fmt.Fprintf(w, `{"data":[{"id":"shared-runtime-model","loaded_context_length":%d}]}`, window)
	}))
	defer server.Close()
	first := Config{Provider: ProviderOpenAI, BaseURL: server.URL + "/v1", APIKey: "first-key", Model: "shared-runtime-model"}
	duplicate := first
	duplicate.BaseURL += "/"
	duplicate.Model = "another-model-at-same-endpoint"
	second := first
	second.Provider, second.APIKey = ProviderResponses, "second-key"
	if err := RefreshLocalContextWindows(context.Background(), first, duplicate, second); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests["Bearer first-key"] != 1 || requests["Bearer second-key"] != 1 || len(requests) != 2 {
		t.Fatalf("native requests=%v", requests)
	}
	if got := llm.ProviderRuntimeContext(first.BaseURL, first.APIKey, first.Model); got != 4096 {
		t.Fatalf("first credential runtime window=%d", got)
	}
	if got := llm.ProviderRuntimeContext(second.BaseURL, second.APIKey, second.Model); got != 8192 {
		t.Fatalf("second credential runtime window=%d", got)
	}
}

func TestRefreshLocalContextWindowsSkipsOtherProtocolsAndCanceledOperation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.NotFound(w, r)
	}))
	defer server.Close()
	for _, provider := range []string{ProviderEcho, ProviderAnthropic, ProviderCodex, ProviderOpencode} {
		if err := RefreshLocalContextWindows(context.Background(), Config{Provider: provider, BaseURL: server.URL + "/v1"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := RefreshLocalContextWindows(context.Background(), Config{Provider: ProviderOpenAI, BaseURL: "https://api.openai.com/v1"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RefreshLocalContextWindows(ctx, Config{Provider: ProviderOpenAI, BaseURL: server.URL + "/v1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled metadata refresh=%v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("skipped operations made %d requests", got)
	}
}

func TestRefreshLocalContextWindowsCallerCancellationStopsWait(t *testing.T) {
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	completed := make(chan error, 1)
	go func() {
		completed <- RefreshLocalContextWindows(ctx, Config{Provider: ProviderOpenAI, BaseURL: server.URL + "/v1"})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("metadata refresh did not start")
	}
	cancel()
	select {
	case err := <-completed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled metadata refresh=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("metadata refresh ignored caller cancellation")
	}
}

func TestRefreshLocalContextWindowsDiscoveryFailureKeepsFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	if err := RefreshLocalContextWindows(context.Background(), Config{Provider: ProviderOpenAI, BaseURL: server.URL + "/v1"}); err != nil {
		t.Fatalf("optional discovery blocked a user operation: %v", err)
	}
}
