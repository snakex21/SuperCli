package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const modelContextWindowsFile = "model-context-windows.json"

// ModelContextOverride is one provider-scoped working-context budget. Provider
// is the configured connection name (for example "anyrouter" or "openai"),
// not merely the transport type, so identical model IDs on two APIs never
// share an override accidentally.
type ModelContextOverride struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Tokens   int    `json:"tokens"`
}

type modelContextFile struct {
	Version int                    `json:"version"`
	Entries []ModelContextOverride `json:"entries"`
}

// ModelContextStore is the instance-local, concurrency-safe store used by TUI,
// WebGUI, and batch. It lives in this SuperCli copy's data directory.
type ModelContextStore struct {
	mu      sync.RWMutex
	path    string
	entries map[string]ModelContextOverride
}

func LoadModelContextStore(dataDir string) *ModelContextStore {
	s := &ModelContextStore{
		path:    filepath.Join(dataDir, modelContextWindowsFile),
		entries: make(map[string]ModelContextOverride),
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	var file modelContextFile
	if json.Unmarshal(data, &file) != nil {
		return s
	}
	for _, entry := range file.Entries {
		entry.Provider = strings.TrimSpace(entry.Provider)
		entry.Model = strings.TrimSpace(entry.Model)
		if entry.Provider != "" && entry.Model != "" && entry.Tokens > 0 {
			s.entries[modelContextKey(entry.Provider, entry.Model)] = entry
		}
	}
	return s
}

func modelContextKey(provider, model string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + "\x00" + strings.TrimSpace(model)
}

func (s *ModelContextStore) Get(provider, model string) (int, bool) {
	if s == nil {
		return 0, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.entries[modelContextKey(provider, model)]
	return entry.Tokens, ok
}

func (s *ModelContextStore) Set(provider, model string, tokens int) error {
	if s == nil {
		return errors.New("model context store is unavailable")
	}
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return errors.New("provider and model are required")
	}
	if tokens <= 0 {
		return errors.New("context budget must be greater than zero")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[modelContextKey(provider, model)] = ModelContextOverride{
		Provider: provider, Model: model, Tokens: tokens,
	}
	return s.saveLocked()
}

// Remove returns true when an exact provider/model override existed.
func (s *ModelContextStore) Remove(provider, model string) (bool, error) {
	if s == nil {
		return false, errors.New("model context store is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := modelContextKey(provider, model)
	if _, ok := s.entries[key]; !ok {
		return false, nil
	}
	delete(s.entries, key)
	return true, s.saveLocked()
}

func (s *ModelContextStore) saveLocked() error {
	file := modelContextFile{Version: 1, Entries: make([]ModelContextOverride, 0, len(s.entries))}
	for _, entry := range s.entries {
		file.Entries = append(file.Entries, entry)
	}
	sort.Slice(file.Entries, func(i, j int) bool {
		if file.Entries[i].Provider != file.Entries[j].Provider {
			return file.Entries[i].Provider < file.Entries[j].Provider
		}
		return file.Entries[i].Model < file.Entries[j].Model
	})
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

// ParseContextBudget accepts plain token counts and convenient k/m suffixes.
// "auto" and "0" mean remove the manual override.
func ParseContextBudget(value string) (tokens int, automatic bool, err error) {
	raw := strings.ToLower(strings.TrimSpace(value))
	if raw == "" || raw == "auto" || raw == "0" {
		return 0, true, nil
	}
	multiplier := int64(1)
	if strings.HasSuffix(raw, "k") {
		multiplier = 1_000
		raw = strings.TrimSpace(strings.TrimSuffix(raw, "k"))
	} else if strings.HasSuffix(raw, "m") {
		multiplier = 1_000_000
		raw = strings.TrimSpace(strings.TrimSuffix(raw, "m"))
	}
	n, parseErr := strconv.ParseInt(raw, 10, 64)
	if parseErr != nil || n <= 0 {
		return 0, false, fmt.Errorf("invalid context budget %q (use e.g. 100k, 131072, 1m, or auto)", value)
	}
	if n > int64(^uint(0)>>1)/multiplier {
		return 0, false, errors.New("context budget is too large")
	}
	n *= multiplier
	if n > 10_000_000 {
		return 0, false, errors.New("context budget cannot exceed 10m tokens")
	}
	return int(n), false, nil
}

// RuntimeProviderName resolves the configured connection identity backing cfg.
// It deliberately distinguishes two profiles with the same transport/model.
func RuntimeProviderName(tc TomlConfig, cfg Config) string {
	if tc.DefaultProvider != "" {
		for _, p := range tc.Providers {
			if p.Name == tc.DefaultProvider && !p.Disabled {
				return p.Name
			}
		}
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	match := ""
	for _, p := range tc.Providers {
		if p.Disabled || p.Type != cfg.Provider || strings.TrimRight(p.BaseURL, "/") != base {
			continue
		}
		if p.Model == cfg.Model {
			return p.Name
		}
		if match == "" {
			match = p.Name
		} else {
			match = "" // ambiguous connection identity
			break
		}
	}
	if match != "" {
		return match
	}
	return cfg.Provider
}
