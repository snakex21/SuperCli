package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultOpencodeBaseURL is the standard port for
// Ollama-style local gateways. The opencode
// headless server (F15) binds here by default.
const DefaultOpencodeBaseURL = "http://localhost:11434/v1"

// OpencodeConfig configures the F15 opencode
// headless provider. The wire format is
// identical to OpenAI (/v1/chat/completions +
// /v1/models) but APIKey is optional (local
// services don't need it) and Model can be
// discovered at runtime.
type OpencodeConfig struct {
	// BaseURL is the gateway root, no trailing
	// slash. Defaults to
	// DefaultOpencodeBaseURL.
	BaseURL string

	// APIKey is the bearer token. Empty is
	// allowed for local services (sent as
	// "not-needed" — harmless to Ollama,
	// llama.cpp, etc.).
	APIKey string

	// Model is the model id. Empty means
	// "discover later" — ProbeModels will fill
	// it from /v1/models. If both Model and
	// discovery fail, the provider cannot be
	// used for chat but can still be used for
	// model listing.
	Model string

	// MaxTokens caps generated tokens; zero keeps the gateway default.
	MaxTokens int

	// Capabilities, if nil, defaults to a
	// fresh registry. Pass the main registry
	// so discovered models land in the
	// F16 capability pool.
	Capabilities *CapabilityRegistry
}

// OpencodeProvider wraps an OpenAI-compat
// provider for the opencode headless gateway
// (F15). The gateway runs locally and exposes
// /v1/chat/completions + /v1/models on a single
// port, aggregating free models from Ollama,
// OpenRouter, Groq, etc.
//
// Key differences from a plain OpenAI provider:
//   - APIKey is optional (empty = "not-needed"
//     for local services)
//   - Model IDs are prefixed with "opencode/"
//     in the capability registry for namespace
//     isolation, but the raw name is sent to the
//     gateway in the API call
//   - Model discovery via ProbeModels() is
//     supported (GET /v1/models)
type OpencodeProvider struct {
	inner    *OpenAIProvider
	caps     *CapabilityRegistry
	model    string // the prefixed model ID, e.g. "opencode/llama3.2" — used by Name()
	rawModel string // the unprefixed model name, e.g. "llama3.2" — sent in API calls
}

// NewOpencode returns an OpencodeProvider. When
// Model is empty the provider can still be used
// for ProbeModels but Complete will fail until a
// model is discovered and the provider is
// re-created. When APIKey is empty, a dummy
// "not-needed" key is used (harmless to local
// services that don't check auth).
func NewOpencode(cfg OpencodeConfig) (*OpencodeProvider, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultOpencodeBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")

	apiKey := CleanAPIKey(cfg.APIKey)
	if apiKey == "" {
		apiKey = "not-needed"
	}

	model := cfg.Model
	if model == "" {
		model = "default"
	}

	// Compute the prefixed ID for registry
	// namespace isolation. The gateway only
	// knows the raw name, so the inner OpenAI
	// provider is configured with rawModel.
	prefixed := model
	if !strings.HasPrefix(model, "opencode/") {
		prefixed = "opencode/" + model
	}
	rawModel := strings.TrimPrefix(model, "opencode/")

	caps := cfg.Capabilities
	if caps == nil {
		caps = NewCapabilityRegistry()
	}

	// The inner OpenAI provider sends rawModel
	// in the JSON body so the gateway recognizes
	// it. Its cfg.Model is rawModel.
	inner, err := NewOpenAI(OpenAIConfig{
		BaseURL:         cfg.BaseURL,
		APIKey:          apiKey,
		Model:           rawModel,
		MaxTokens:       cfg.MaxTokens,
		Capabilities:    caps,
		CapabilityModel: prefixed,
	})
	if err != nil {
		return nil, fmt.Errorf("llm.NewOpencode: %w", err)
	}
	return &OpencodeProvider{
		inner:    inner,
		caps:     caps,
		model:    prefixed,
		rawModel: rawModel,
	}, nil
}

// Name implements Provider. Returns the prefixed
// model ID (e.g. "opencode/llama3.2").
func (o *OpencodeProvider) Name() string { return o.model }

// Complete implements Provider. Delegates to the
// inner OpenAI-compat client. The prefixed
// model ID is sent in the JSON body; the
// gateway routes it to the correct backend.
func (o *OpencodeProvider) Complete(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan Delta, error) {
	return o.inner.Complete(ctx, msgs, tools)
}

// SupportsVision returns true when the model is
// known to handle image inputs. F15 opencode
// models are registered without vision by
// default; ProbeModels can update this later.
func (o *OpencodeProvider) SupportsVision() bool {
	return o.caps.HasVision(o.model)
}

// opencodeModelsResponse is the /v1/models
// response shape. Standard OpenAI format.
type opencodeModelsResponse struct {
	Data []opencodeModelEntry `json:"data"`
}

type opencodeModelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}

// ProbeModels fetches /v1/models from the
// gateway and returns one ModelInfo per
// discovered model. Each model's ID is prefixed
// with "opencode/" to keep the namespace
// isolated. Discovered models are also registered
// in the capability registry automatically.
//
// Returns nil, nil when the endpoint is
// unreachable or returns a non-200 status. This
// is a soft failure — the provider can still be
// used with an explicit --model flag.
func (o *OpencodeProvider) ProbeModels(ctx context.Context) ([]ModelInfo, error) {
	// Build the /v1/models URL from the inner
	// provider's configured base URL. The
	// OpenAI provider's BaseURL already includes
	// the /v1 suffix (e.g. "http://host:port/v1"),
	// so we just append /models.
	base := o.inner.cfg.BaseURL
	url := strings.TrimRight(base, "/") + "/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("opencode probe: %w", err)
	}
	// Some gateways check auth even for
	// /v1/models; send the bearer token.
	if key := CleanAPIKey(o.inner.cfg.APIKey); key != "" && key != "not-needed" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	client := o.inner.http
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		// Soft failure: gateway unreachable.
		return nil, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB cap
	if err != nil {
		return nil, nil
	}

	var parsed opencodeModelsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, nil
	}

	out := make([]ModelInfo, 0, len(parsed.Data))
	for _, e := range parsed.Data {
		id := strings.TrimSpace(e.ID)
		if id == "" {
			continue
		}
		prefixed := "opencode/" + id
		info := ModelInfo{
			ID:       prefixed,
			Provider: "opencode",
			Source:   SourceOpencode,
			Stream:   true,
			ToolUse:  true,
		}
		o.caps.Register(info)
		out = append(out, info)
	}
	return out, nil
}

// ProbeModels standalone function fetches
// /v1/models from an arbitrary base URL and
// returns model entries. Useful for testing and
// for main.go's startup probe before the
// provider is fully constructed.
func ProbeOpencodeModels(ctx context.Context, baseURL, apiKey string) ([]opencodeModelEntry, error) {
	if baseURL == "" {
		baseURL = DefaultOpencodeBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	// If the caller passed a base URL that already
	// ends with /v1, append /models. If not, append
	// /v1/models. The OpenAI provider stores the
	// full /v1 path in BaseURL, but standalone
	// callers may pass just the host.
	url := baseURL + "/models"
	if !strings.HasSuffix(baseURL, "/v1") {
		url = baseURL + "/v1/models"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if key := CleanAPIKey(apiKey); key != "" && key != "not-needed" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opencode probe: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var parsed opencodeModelsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	return parsed.Data, nil
}
