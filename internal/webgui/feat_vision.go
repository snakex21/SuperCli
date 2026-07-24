package webgui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"supercli/internal/llm"
)

const maxVisionRequestBytes = 32 << 20

type visionTranscribeRequest struct {
	ImageBase64 string `json:"imageBase64"`
	MimeType    string `json:"mimeType"`
	Prompt      string `json:"prompt"`
	ProviderID  string `json:"providerId"`
	ModelID     string `json:"modelId"`
}

func (s *Server) handleVisionTranscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxVisionRequestBytes)
	var req visionTranscribeRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.ProviderID = strings.TrimSpace(req.ProviderID)
	req.ModelID = strings.TrimSpace(req.ModelID)
	req.MimeType = strings.TrimSpace(req.MimeType)
	req.ImageBase64 = strings.TrimSpace(req.ImageBase64)
	if req.Prompt == "" {
		req.Prompt = "Transcribe all text from this document image. Preserve line breaks and paragraphs. Return only the transcribed text."
	}
	if strings.HasPrefix(req.ImageBase64, "data:") {
		media, data, ok := splitDataImage(req.ImageBase64)
		if !ok {
			http.Error(w, "invalid image data URL", http.StatusBadRequest)
			return
		}
		if req.MimeType == "" {
			req.MimeType = media
		}
		req.ImageBase64 = data
	}
	if req.ImageBase64 != "" {
		if req.MimeType == "" {
			req.MimeType = "image/png"
		}
		if !allowedVisionMIME(req.MimeType) {
			http.Error(w, "unsupported image type", http.StatusBadRequest)
			return
		}
		if _, err := base64.StdEncoding.DecodeString(req.ImageBase64); err != nil {
			http.Error(w, "invalid base64 image", http.StatusBadRequest)
			return
		}
	}

	provider, err := s.eng.providerForSelection(req.ModelID, req.ProviderID, "vision")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	message := llm.Message{Role: llm.RoleUser, Content: req.Prompt}
	if req.ImageBase64 != "" {
		message.Content = ""
		message.Parts = []llm.ContentPart{
			{Type: llm.PartTypeText, Text: req.Prompt},
			{Type: llm.PartTypeImage, Image: &llm.ImageRef{Data: req.ImageBase64, MediaType: req.MimeType}},
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	stream, err := provider.Complete(llm.WithPurpose(ctx, "vision"), []llm.Message{message}, nil)
	if err != nil {
		http.Error(w, "vision request: "+err.Error(), http.StatusBadGateway)
		return
	}
	var text strings.Builder
	for delta := range stream {
		if delta.Err != nil {
			http.Error(w, "vision request: "+delta.Err.Error(), http.StatusBadGateway)
			return
		}
		text.WriteString(delta.Content)
	}
	result := stripThinking(strings.TrimSpace(text.String()))
	if result == "" {
		http.Error(w, "model returned no final text", http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]string{"text": result})
}

func (e *Engine) providerForSelection(modelID, providerName, purpose string) (llm.Provider, error) {
	e.mu.RLock()
	activeProvider := e.prov
	cfg := e.cfg
	e.mu.RUnlock()
	if modelID == "" && providerName == "" {
		if activeProvider == nil {
			return nil, fmt.Errorf("no active provider")
		}
		return activeProvider, nil
	}
	currentProvider, currentModel, _ := e.RuntimeSelection()
	if modelID == currentModel && (providerName == "" || providerName == currentProvider || providerName == cfg.Provider) {
		if activeProvider == nil {
			return nil, fmt.Errorf("no active provider")
		}
		return activeProvider, nil
	}
	if modelID == "" {
		modelID = cfg.Model
	}
	if providerName == "" {
		providerName = e.caps.Provider(modelID)
	}
	cfg.Model = modelID
	found := false
	for _, provider := range e.providerManager().Configured() {
		if provider.Name != providerName {
			continue
		}
		if provider.Disabled {
			return nil, fmt.Errorf("provider %q is disabled", providerName)
		}
		found = true
		cfg.Provider = provider.Type
		cfg.BaseURL = provider.BaseURL
		cfg.APIKey = provider.APIKey
		break
	}
	if providerName != "" && !found {
		return nil, fmt.Errorf("provider %q is not configured", providerName)
	}
	if err := cfg.Normalize(); err != nil {
		return nil, err
	}
	if purpose == "" {
		purpose = llm.PurposeMain
	}
	return e.factory.Build(cfg, purpose)
}

func splitDataImage(dataURL string) (mediaType, data string, ok bool) {
	header, body, found := strings.Cut(dataURL, ",")
	if !found || !strings.HasPrefix(header, "data:") || !strings.HasSuffix(strings.ToLower(header), ";base64") {
		return "", "", false
	}
	mediaType = strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
	return strings.ToLower(strings.TrimSpace(mediaType)), strings.TrimSpace(body), true
}

func allowedVisionMIME(mediaType string) bool {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return true
	default:
		return false
	}
}
