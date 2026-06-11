package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// LocalServer describes a locally running LLM server discovered
// by DetectLocalServers (Ollama or LM Studio).
type LocalServer struct {
	// Name is the provider name to store in config.toml
	// ("ollama" or "lmstudio").
	Name string
	// Label is the human-readable name for menus.
	Label string
	// Type is the provider type for config.toml (always
	// "openai": both servers speak the OpenAI protocol on
	// their /v1 endpoint).
	Type string
	// BaseURL is the OpenAI-compatible endpoint root
	// (e.g. http://localhost:11434/v1).
	BaseURL string
	// Models are the model IDs the server reported.
	Models []string
}

// Default endpoints for local server detection.
const (
	OllamaDefaultURL   = "http://localhost:11434"
	LMStudioDefaultURL = "http://localhost:1234"

	// localDetectTimeout caps each probe. Local servers answer in
	// milliseconds; a short timeout keeps first-run startup snappy
	// when nothing is listening.
	localDetectTimeout = 1500 * time.Millisecond
)

// DetectLocalServers probes Ollama (GET /api/tags) and LM Studio
// (GET /v1/models) in parallel with short timeouts and returns
// the servers that responded, each with its list of available
// models. Servers that are down or respond with garbage are
// silently omitted. The result order is stable: ollama first,
// then lmstudio.
func DetectLocalServers(ctx context.Context) []LocalServer {
	return detectLocalServers(ctx, OllamaDefaultURL, LMStudioDefaultURL)
}

// detectLocalServers is the testable core: base URLs are injected.
func detectLocalServers(ctx context.Context, ollamaURL, lmstudioURL string) []LocalServer {
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		found []LocalServer
	)
	add := func(s LocalServer) {
		mu.Lock()
		found = append(found, s)
		mu.Unlock()
	}

	wg.Add(2)
	go func() {
		defer wg.Done()
		if models, err := probeOllama(ctx, ollamaURL); err == nil {
			add(LocalServer{
				Name:    "ollama",
				Label:   "Ollama (local)",
				Type:    "openai",
				BaseURL: strings.TrimRight(ollamaURL, "/") + "/v1",
				Models:  models,
			})
		}
	}()
	go func() {
		defer wg.Done()
		if models, err := probeLMStudio(ctx, lmstudioURL); err == nil {
			add(LocalServer{
				Name:    "lmstudio",
				Label:   "LM Studio (local)",
				Type:    "openai",
				BaseURL: strings.TrimRight(lmstudioURL, "/") + "/v1",
				Models:  models,
			})
		}
	}()
	wg.Wait()

	// Deterministic order: ollama first, then lmstudio.
	sort.Slice(found, func(i, j int) bool { return found[i].Name > found[j].Name })
	return found
}

// probeOllama hits GET <base>/api/tags and returns model names.
func probeOllama(ctx context.Context, baseURL string) ([]string, error) {
	raw, err := localGET(ctx, strings.TrimRight(baseURL, "/")+"/api/tags")
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("ollama /api/tags: %w", err)
	}
	models := make([]string, 0, len(parsed.Models))
	for _, m := range parsed.Models {
		if m.Name != "" {
			models = append(models, m.Name)
		}
	}
	sort.Strings(models)
	return models, nil
}

// probeLMStudio hits GET <base>/v1/models and returns model IDs.
func probeLMStudio(ctx context.Context, baseURL string) ([]string, error) {
	base := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	raw, err := localGET(ctx, base+"/models")
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("lmstudio /v1/models: %w", err)
	}
	models := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	sort.Strings(models)
	return models, nil
}

// localGET performs a GET with the local-detection timeout and
// returns the body on 2xx.
func localGET(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, localDetectTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
