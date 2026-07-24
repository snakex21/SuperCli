package webgui

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"supercli/internal/storage/memory"
)

func defaultFolderIndexConfig() folderIndexConfig {
	return folderIndexConfig{
		SelectedPaths: []string{},
		CustomPaths:   []string{},
		Indexed:       map[string]folderIndexedInfo{},
		OutlookFolder: "Inbox",
		OutlookMax:    250,
	}
}

func loadFolderIndexConfig(dataDir string) folderIndexConfig {
	config := defaultFolderIndexConfig()
	data, err := os.ReadFile(filepath.Join(dataDir, folderIndexFile))
	if err != nil {
		return config
	}
	if json.Unmarshal(data, &config) != nil {
		return defaultFolderIndexConfig()
	}
	if config.SelectedPaths == nil {
		config.SelectedPaths = []string{}
	}
	if config.CustomPaths == nil {
		config.CustomPaths = []string{}
	}
	if config.Indexed == nil {
		config.Indexed = map[string]folderIndexedInfo{}
	}
	if strings.TrimSpace(config.OutlookFolder) == "" {
		config.OutlookFolder = "Inbox"
	}
	if config.OutlookMax <= 0 {
		config.OutlookMax = 250
	}
	return config
}

func saveFolderIndexConfig(dataDir string, config folderIndexConfig) error {
	config.SelectedPaths = uniqueCleanPaths(config.SelectedPaths)
	config.CustomPaths = uniqueCleanPaths(config.CustomPaths)
	config.Enabled = len(config.SelectedPaths) > 0 || config.OutlookIndex
	config.VisionProvider = strings.TrimSpace(config.VisionProvider)
	config.VisionModel = strings.TrimSpace(config.VisionModel)
	config.OutlookFolder = strings.TrimSpace(config.OutlookFolder)
	if config.OutlookFolder == "" {
		config.OutlookFolder = "Inbox"
	}
	if config.OutlookMax <= 0 {
		config.OutlookMax = 250
	}
	if config.OutlookMax > 2000 {
		config.OutlookMax = 2000
	}
	if config.Indexed == nil {
		config.Indexed = map[string]folderIndexedInfo{}
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, folderIndexFile), data, 0o644)
}

func uniqueCleanPaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := filepath.Clean(strings.TrimSpace(raw))
		if path == "." || !filepath.IsAbs(path) {
			continue
		}
		key := strings.ToLower(path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, path)
	}
	return out
}

func folderIndexLocations() []folderIndexLocation {
	folderLocationsCache.Lock()
	if len(folderLocationsCache.locations) > 0 && time.Since(folderLocationsCache.checkedAt) < folderLocationsCacheTTL {
		locations := append([]folderIndexLocation(nil), folderLocationsCache.locations...)
		folderLocationsCache.Unlock()
		return locations
	}
	folderLocationsCache.Unlock()

	locations := []folderIndexLocation{}
	add := func(label, path, kind string) {
		path = filepath.Clean(path)
		if path == "." || !filepath.IsAbs(path) {
			return
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return
		}
		locations = append(locations, folderIndexLocation{Label: label, Path: path, Kind: kind})
	}
	for _, location := range suggestedUserFolders() {
		add(location.Label, location.Path, location.Kind)
	}
	for _, root := range logicalDriveRoots() {
		add("Dysk "+strings.TrimSuffix(root, `\`), root, "suggested")
	}
	folderLocationsCache.Lock()
	folderLocationsCache.locations = append([]folderIndexLocation(nil), locations...)
	folderLocationsCache.checkedAt = time.Now()
	folderLocationsCache.Unlock()
	return append([]folderIndexLocation(nil), locations...)
}

func folderIndexEntries(config folderIndexConfig) []folderIndexEntry {
	selected := map[string]bool{}
	for _, path := range config.SelectedPaths {
		selected[strings.ToLower(filepath.Clean(path))] = true
	}
	locations := folderIndexLocations()
	locationByPath := map[string]folderIndexLocation{}
	for _, location := range locations {
		locationByPath[strings.ToLower(filepath.Clean(location.Path))] = location
	}
	entries := []folderIndexEntry{}
	seen := map[string]bool{}
	add := func(path string) {
		path = filepath.Clean(path)
		if path == "." || !filepath.IsAbs(path) {
			return
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return
		}
		key := strings.ToLower(path)
		if seen[key] {
			return
		}
		seen[key] = true
		label := filepath.Base(path)
		if label == "." || label == string(filepath.Separator) {
			label = path
		}
		kind := "saved"
		if location, ok := locationByPath[key]; ok {
			label = location.Label
			kind = location.Kind
		}
		idHash := sha1.Sum([]byte(key))
		entry := folderIndexEntry{
			ID:       "folder-" + hex.EncodeToString(idHash[:])[:10],
			Label:    label,
			Path:     path,
			Kind:     kind,
			Selected: selected[key],
		}
		if indexed, ok := config.Indexed[path]; ok {
			copy := indexed
			entry.Indexed = &copy
		}
		entries = append(entries, entry)
	}

	for _, path := range config.CustomPaths {
		add(path)
	}
	for _, path := range config.SelectedPaths {
		add(path)
	}
	return entries
}

func folderIndexSuggestions(config folderIndexConfig) []folderIndexLocation {
	configured := map[string]bool{}
	for _, path := range append(append([]string{}, config.CustomPaths...), config.SelectedPaths...) {
		configured[strings.ToLower(filepath.Clean(path))] = true
	}
	suggestions := []folderIndexLocation{}
	for _, location := range folderIndexLocations() {
		if !configured[strings.ToLower(filepath.Clean(location.Path))] {
			suggestions = append(suggestions, location)
		}
	}
	return suggestions
}

func removeFolderManifest(dataDir, home, path string) {
	store, err := memory.OpenProjectStore(dataDir, home)
	if err != nil {
		return
	}
	defer store.Close()
	if entries, err := store.ByTag(folderIndexTag(path), 200); err == nil {
		for _, entry := range entries {
			_ = store.Delete(entry.ID)
		}
	}
}
