package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// runtimeContextAnyModel is the snapshot key for a single-model server that
// serves whatever model name a request carries (llama.cpp llama-server). The
// loaded n_ctx then applies to every model id sent to that endpoint.
const runtimeContextAnyModel = "*"

// localServerRuntimeTimeout bounds each llama.cpp / Ollama metadata request.
// They answer from memory; the surrounding refresh has its own deadline.
const localServerRuntimeTimeout = 2 * time.Second

// fetchLocalServerRuntimeContexts reads the loaded context size from local
// servers that do not speak LM Studio's /api/v{0,1}/models:
//
//   - llama.cpp llama-server: GET /props -> default_generation_settings.n_ctx
//     (the per-slot context actually allocated, not n_ctx_train). Router
//     builds serving several models answer /props?model=<id> per model.
//   - Ollama: GET /api/ps -> models[].context_length of loaded models.
//
// Without this, SuperCli fell back to a 16k guess on these servers while the
// server may truncate silently at a smaller n_ctx (Ollama's default) or waste
// a larger one. supported=false means neither API answered; an Ollama with no
// model loaded yet is supported with no windows, so a later refresh retries.
func fetchLocalServerRuntimeContexts(ctx context.Context, baseURL, apiKey string) (models []ModelInfo, supported bool) {
	root := localNativeModelsRoot(baseURL)
	if root == "" {
		return nil, false
	}
	if models, ok := fetchLlamaServerRuntimeContexts(ctx, root, apiKey); ok {
		return models, true
	}
	return fetchOllamaRuntimeContexts(ctx, root, apiKey)
}

func localServerGet(ctx context.Context, rawURL, apiKey string, into any) bool {
	if ctx.Err() != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false
	}
	if key := CleanAPIKey(apiKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := (&http.Client{Timeout: localServerRuntimeTimeout}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return false
	}
	return json.Unmarshal(body, into) == nil
}

type llamaServerProps struct {
	NCtx                      json.RawMessage `json:"n_ctx"`
	DefaultGenerationSettings *struct {
		NCtx json.RawMessage `json:"n_ctx"`
	} `json:"default_generation_settings"`
}

func (p llamaServerProps) contextLength() int {
	if p.DefaultGenerationSettings != nil {
		if n := positiveRuntimeContextLength(p.DefaultGenerationSettings.NCtx); n > 0 {
			return n
		}
	}
	return positiveRuntimeContextLength(p.NCtx)
}

func fetchLlamaServerRuntimeContexts(ctx context.Context, root, apiKey string) ([]ModelInfo, bool) {
	var props llamaServerProps
	if !localServerGet(ctx, root+"/props", apiKey, &props) {
		return nil, false
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = localServerGet(ctx, root+"/v1/models", apiKey, &list)
	ids := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		if id := strings.TrimSpace(m.ID); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) > 1 {
		// Router mode: each model has its own instance and n_ctx. Never let
		// one model's capacity stand in for another.
		var out []ModelInfo
		for _, id := range ids {
			var per llamaServerProps
			if localServerGet(ctx, root+"/props?model="+url.QueryEscape(id), apiKey, &per) {
				if n := per.contextLength(); n > 0 {
					out = append(out, ModelInfo{ID: id, RuntimeContextLength: n})
				}
			}
		}
		return out, true
	}
	n := props.contextLength()
	if n <= 0 {
		// A /props without n_ctx is not llama-server's; let Ollama try.
		return nil, false
	}
	out := []ModelInfo{{ID: runtimeContextAnyModel, RuntimeContextLength: n}}
	for _, id := range ids {
		out = append(out, ModelInfo{ID: id, RuntimeContextLength: n})
	}
	return out, true
}

func fetchOllamaRuntimeContexts(ctx context.Context, root, apiKey string) ([]ModelInfo, bool) {
	var ps struct {
		Models *[]struct {
			Name          string          `json:"name"`
			Model         string          `json:"model"`
			ContextLength json.RawMessage `json:"context_length"`
		} `json:"models"`
	}
	if !localServerGet(ctx, root+"/api/ps", apiKey, &ps) || ps.Models == nil {
		return nil, false
	}
	var out []ModelInfo
	seen := map[string]bool{}
	for _, m := range *ps.Models {
		n := positiveRuntimeContextLength(m.ContextLength)
		if n <= 0 {
			continue
		}
		for _, id := range []string{strings.TrimSpace(m.Name), strings.TrimSpace(m.Model)} {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, ModelInfo{ID: id, RuntimeContextLength: n})
			// Ollama accepts "qwen3" for "qwen3:latest".
			if base, ok := strings.CutSuffix(id, ":latest"); ok && !seen[base] {
				seen[base] = true
				out = append(out, ModelInfo{ID: base, RuntimeContextLength: n})
			}
		}
	}
	return out, true
}
