package uilang

import (
	"embed"
	"encoding/json"
	"fmt"
	"sync"
)

// Catalogs are portable application assets compiled into the executable.
//
//go:embed catalogs/*.json
var catalogFiles embed.FS

type catalogEntry struct {
	code     string
	once     sync.Once
	messages map[string]string
}

type catalogStore struct {
	entries []catalogEntry
	index   map[string]int // immutable after initialization
}

func newCatalogStore() *catalogStore {
	store := &catalogStore{entries: make([]catalogEntry, len(languages)), index: make(map[string]int, len(languages))}
	for i, language := range languages {
		store.entries[i].code = language.Code
		store.index[language.Code] = i
	}
	return store
}

// Initialization creates metadata only. A CLI flag or GUI route that never
// displays a localized label pays no JSON parsing or catalog allocation cost.
var defaultCatalogStore = newCatalogStore()

func (entry *catalogEntry) load() map[string]string {
	entry.once.Do(func() {
		data, err := catalogFiles.ReadFile("catalogs/" + entry.code + ".json")
		if err != nil {
			return
		}
		var messages map[string]string
		if json.Unmarshal(data, &messages) == nil {
			entry.messages = messages
		}
	})
	return entry.messages
}

func (store *catalogStore) catalog(language string) map[string]string {
	index, supported := store.index[language]
	if !supported {
		index, supported = store.index[Normalize(language)]
	}
	if !supported {
		index = store.index[English]
	}
	return store.entries[index].load()
}

func (store *catalogStore) text(language, key string) string {
	if value := store.catalog(language)[key]; value != "" {
		return value
	}
	if value := store.catalog(English)[key]; value != "" {
		return value
	}
	return key
}

// Text returns an interface message with safe English fallback. Model prompts,
// command arguments, and tool schemas remain independent of UI localization.
func Text(language, key string) string {
	return defaultCatalogStore.text(language, key)
}

// Format uses Go printf placeholders, checked for parity in every locale.
func Format(language, key string, args ...any) string {
	return fmt.Sprintf(Text(language, key), args...)
}

// ProviderDescription localizes only built-in preset descriptions. Connection
// names, URLs, protocol types and model identifiers remain backend data.
func ProviderDescription(language, name, fallback string) string {
	key := "provider.description." + name
	if description := Text(language, key); description != key {
		return description
	}
	return fallback
}
