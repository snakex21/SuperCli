package llm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	ModelTransportOpenAICompatible = "openai-compatible"
	ModelTransportResponses        = "responses"
	ModelTransportAnthropic        = "anthropic"
	ModelTransportGoogle           = "google"

	openCodeZenCatalogURL  = "https://models.dev/api.json"
	openCodeZenCatalogFile = "opencode_zen_catalog.json"
	openCodeZenCatalogTTL  = 24 * time.Hour
)

type openCodeZenCatalog struct {
	FetchedAt time.Time            `json:"fetched_at"`
	Models    map[string]ModelInfo `json:"models"`
}

var openCodeZenCatalogMu sync.Mutex

// ResolveOpenCodeZenModelMetadata resolves protocol and capability metadata
// from the same models.dev provider catalog consumed by OpenCode. The compact
// result is cached beside the rest of SuperCLI's portable data. Model names are
// never used to choose a transport, so newly published Zen models work as soon
// as the upstream catalog describes them.
func ResolveOpenCodeZenModelMetadata(dataDir, baseURL, model string, caps *CapabilityRegistry) (ModelInfo, bool) {
	if !isOpenCodeZenBaseURL(baseURL) || strings.TrimSpace(model) == "" {
		return ModelInfo{}, false
	}
	if caps != nil {
		if info, ok := caps.Get(model); ok && info.Transport != "" && info.ReasoningKnown {
			return info, true
		}
	}

	openCodeZenCatalogMu.Lock()
	defer openCodeZenCatalogMu.Unlock()

	cached, cachedOK := loadOpenCodeZenCatalog(dataDir)
	if cachedOK && time.Since(cached.FetchedAt) <= openCodeZenCatalogTTL {
		if info, ok := registerOpenCodeZenMetadata(cached.Models, model, caps); ok {
			return info, true
		}
		// A model newly advertised by /models may not exist in an otherwise
		// fresh daily cache. Refresh immediately on that cache miss so a newly
		// published free model does not wait for the TTL to expire.
	}

	fresh, err := fetchOpenCodeZenCatalog()
	if err == nil {
		_ = saveOpenCodeZenCatalog(dataDir, fresh)
		return registerOpenCodeZenMetadata(fresh.Models, model, caps)
	}
	if cachedOK {
		return registerOpenCodeZenMetadata(cached.Models, model, caps)
	}
	return ModelInfo{}, false
}

func registerOpenCodeZenMetadata(models map[string]ModelInfo, model string, caps *CapabilityRegistry) (ModelInfo, bool) {
	info, ok := models[strings.ToLower(strings.TrimSpace(model))]
	if !ok {
		return ModelInfo{}, false
	}
	info.ID = model
	info.Provider = "opencode"
	info.Source = SourceExternal
	if caps != nil {
		if existing, exists := caps.Get(model); exists {
			existing.Transport = info.Transport
			existing.Reasoning = info.Reasoning
			existing.ReasoningKnown = info.ReasoningKnown
			existing.Vision = existing.Vision || info.Vision
			existing.VisionKnown = existing.VisionKnown || info.VisionKnown
			existing.ToolUse = info.ToolUse
			if info.ContextLength > 0 {
				existing.ContextLength = info.ContextLength
			}
			existing.Source = SourceExternal
			caps.Register(existing)
			return existing, true
		}
		caps.Register(info)
	}
	return info, true
}

func openCodeZenTransport(npm string) string {
	switch strings.ToLower(strings.TrimSpace(npm)) {
	case "@ai-sdk/openai":
		return ModelTransportResponses
	case "@ai-sdk/anthropic":
		return ModelTransportAnthropic
	case "@ai-sdk/google":
		return ModelTransportGoogle
	default:
		return ModelTransportOpenAICompatible
	}
}

func parseOpenCodeZenCatalog(data []byte, fetchedAt time.Time) (openCodeZenCatalog, error) {
	var root map[string]struct {
		NPM    string `json:"npm"`
		Models map[string]struct {
			Provider struct {
				NPM string `json:"npm"`
			} `json:"provider"`
			Reasoning  bool `json:"reasoning"`
			Attachment bool `json:"attachment"`
			ToolCall   bool `json:"tool_call"`
			Limit      struct {
				Context int `json:"context"`
			} `json:"limit"`
			Modalities struct {
				Input []string `json:"input"`
			} `json:"modalities"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return openCodeZenCatalog{}, fmt.Errorf("parse models.dev: %w", err)
	}
	provider, ok := root["opencode"]
	if !ok || len(provider.Models) == 0 {
		return openCodeZenCatalog{}, fmt.Errorf("models.dev: opencode catalog missing")
	}
	models := make(map[string]ModelInfo, len(provider.Models))
	for id, raw := range provider.Models {
		npm := raw.Provider.NPM
		if npm == "" {
			npm = provider.NPM
		}
		vision := raw.Attachment
		for _, modality := range raw.Modalities.Input {
			if strings.EqualFold(modality, "image") {
				vision = true
			}
		}
		models[strings.ToLower(id)] = ModelInfo{
			ID:             id,
			Provider:       "opencode",
			Transport:      openCodeZenTransport(npm),
			Vision:         vision,
			VisionKnown:    true,
			ToolUse:        raw.ToolCall,
			Stream:         true,
			Reasoning:      raw.Reasoning,
			ReasoningKnown: true,
			ContextLength:  raw.Limit.Context,
			Source:         SourceExternal,
			LastVerified:   fetchedAt,
		}
	}
	return openCodeZenCatalog{FetchedAt: fetchedAt, Models: models}, nil
}

func fetchOpenCodeZenCatalog() (openCodeZenCatalog, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, openCodeZenCatalogURL, nil)
	if err != nil {
		return openCodeZenCatalog{}, err
	}
	req.Header.Set("User-Agent", "SuperCLI/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return openCodeZenCatalog{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return openCodeZenCatalog{}, fmt.Errorf("models.dev: http %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return openCodeZenCatalog{}, err
	}
	return parseOpenCodeZenCatalog(data, time.Now().UTC())
}

func loadOpenCodeZenCatalog(dataDir string) (openCodeZenCatalog, bool) {
	if strings.TrimSpace(dataDir) == "" {
		return openCodeZenCatalog{}, false
	}
	data, err := os.ReadFile(filepath.Join(dataDir, openCodeZenCatalogFile))
	if err != nil {
		return openCodeZenCatalog{}, false
	}
	var catalog openCodeZenCatalog
	if json.Unmarshal(data, &catalog) != nil || len(catalog.Models) == 0 {
		return openCodeZenCatalog{}, false
	}
	return catalog, true
}

func saveOpenCodeZenCatalog(dataDir string, catalog openCodeZenCatalog) error {
	if strings.TrimSpace(dataDir) == "" {
		return nil
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(catalog)
	if err != nil {
		return err
	}
	path := filepath.Join(dataDir, openCodeZenCatalogFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
