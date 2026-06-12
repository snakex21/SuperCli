package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Embedder turns text into a dense vector. Implementations must
// be safe for concurrent use. A nil Embedder means "FTS5 only" —
// every caller degrades gracefully.
type Embedder interface {
	// Embed returns the embedding for one text.
	Embed(ctx context.Context, text string) ([]float32, error)
	// Name identifies the backend for status displays,
	// e.g. "ollama (nomic-embed-text)".
	Name() string
}

// probeTimeout bounds the local-server detection pings so startup
// never hangs on a firewalled port.
const probeTimeout = 800 * time.Millisecond

// embedTimeout bounds a single embedding call.
const embedTimeout = 10 * time.Second

// DetectEmbedder picks the best available embedding backend:
//
//  1. Ollama on localhost:11434 (model nomic-embed-text)
//  2. LM Studio on localhost:1234 (OpenAI-compatible /v1/embeddings)
//  3. The OpenAI API, when an API key is configured
//  4. nil — callers fall back to FTS5-only search
//
// Detection does network pings, so call it off the hot path
// (e.g. in a goroutine at startup) and attach the result with
// Store.SetEmbedder.
func DetectEmbedder(openaiKey string) Embedder {
	client := &http.Client{Timeout: probeTimeout}
	// Ollama: GET /api/tags answers when the daemon is up.
	if resp, err := client.Get("http://localhost:11434/api/tags"); err == nil {
		model := pickOllamaEmbedModel(resp.Body)
		resp.Body.Close()
		if model != "" {
			return &ollamaEmbedder{base: "http://localhost:11434", model: model}
		}
	}
	// LM Studio: GET /v1/models answers when the server is up.
	if resp, err := client.Get("http://localhost:1234/v1/models"); err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return &openAIEmbedder{
				base:  "http://localhost:1234",
				model: "nomic-embed-text",
				label: "lmstudio",
			}
		}
	}
	if strings.TrimSpace(openaiKey) != "" {
		return &openAIEmbedder{
			base:  "https://api.openai.com",
			key:   openaiKey,
			model: "text-embedding-3-small",
			label: "openai",
		}
	}
	return nil
}

// pickOllamaEmbedModel scans the /api/tags response for an
// installed embedding model. Returns "" when none is installed —
// we never trigger an implicit model download.
func pickOllamaEmbedModel(r io.Reader) string {
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&tags); err != nil {
		return ""
	}
	preferred := []string{"nomic-embed-text", "mxbai-embed-large", "all-minilm", "bge-", "embed"}
	for _, want := range preferred {
		for _, m := range tags.Models {
			if strings.Contains(strings.ToLower(m.Name), want) {
				return m.Name
			}
		}
	}
	return ""
}

// ollamaEmbedder calls Ollama's native /api/embeddings.
type ollamaEmbedder struct {
	base  string
	model string
}

func (o *ollamaEmbedder) Name() string { return "ollama (" + o.model + ")" }

func (o *ollamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	body, _ := json.Marshal(map[string]string{"model": o.model, "prompt": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: embedTimeout}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embeddings: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("ollama embeddings: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		Embedding []float64 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("ollama embeddings: decode: %w", err)
	}
	return toFloat32(out.Embedding), nil
}

// openAIEmbedder calls an OpenAI-compatible /v1/embeddings
// endpoint (the real API or LM Studio).
type openAIEmbedder struct {
	base  string
	key   string
	model string
	label string
}

func (o *openAIEmbedder) Name() string { return o.label + " (" + o.model + ")" }

func (o *openAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	body, _ := json.Marshal(map[string]any{"model": o.model, "input": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.base, "/")+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.key != "" {
		req.Header.Set("Authorization", "Bearer "+o.key)
	}
	resp, err := (&http.Client{Timeout: embedTimeout}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s embeddings: %w", o.label, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("%s embeddings: HTTP %d: %s", o.label, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%s embeddings: decode: %w", o.label, err)
	}
	if len(out.Data) == 0 {
		return nil, fmt.Errorf("%s embeddings: empty response", o.label)
	}
	return toFloat32(out.Data[0].Embedding), nil
}

func toFloat32(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}
