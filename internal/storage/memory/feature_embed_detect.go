package memory

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Native model metadata distinguishes embeddings from chat models and exposes
// loaded instances. A healthy chat server alone is not an embedding backend.
// Use only an already loaded embedding model, without downloading/loading one.
func detectLMStudioEmbedder(client *http.Client, base string) Embedder {
	for _, endpoint := range []string{"/api/v1/models", "/api/v0/models"} {
		response, err := client.Get(base + endpoint)
		if err != nil {
			return nil
		}
		if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed {
			response.Body.Close()
			continue
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return nil
		}
		model := loadedLMStudioEmbedModel(response.Body)
		response.Body.Close()
		if model == "" {
			return nil
		}
		return &openAIEmbedder{base: base, model: model, label: "lmstudio"}
	}
	return nil
}
func loadedLMStudioEmbedModel(reader io.Reader) string {
	type instance struct {
		ID string `json:"id"`
	}
	type model struct {
		ID     string     `json:"id"`
		Type   string     `json:"type"`
		State  string     `json:"state"`
		Loaded []instance `json:"loaded_instances"`
	}
	var catalog struct {
		Models []model `json:"models"`
		Data   []model `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(reader, 1<<20)).Decode(&catalog); err != nil {
		return ""
	}
	for _, entry := range catalog.Models {
		if entry.Type != "embedding" {
			continue
		}
		for _, loaded := range entry.Loaded {
			if id := strings.TrimSpace(loaded.ID); id != "" {
				return id
			}
		}
	}
	for _, entry := range catalog.Data {
		if entry.Type == "embeddings" && entry.State == "loaded" {
			if id := strings.TrimSpace(entry.ID); id != "" {
				return id
			}
		}
	}
	return ""
}
