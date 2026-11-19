package config

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxProviderNameLength bounds a provider name typed by the user. Names are
// shown in pickers and status bars and appear in "provider/model" references.
const MaxProviderNameLength = 64

// ValidateProviderName checks a NEW provider name (rename target). Names
// may contain letters, digits, spaces and . _ - only. "/" is rejected
// because "provider/model" references split on the first slash, and ":"
// because "/providers toggle <provider>::<model>" uses it as a separator.
// Existing names are never re-validated, so old configs keep loading.
func ValidateProviderName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("provider name is required")
	}
	if name != strings.TrimSpace(name) {
		return errors.New("provider name must not start or end with a space")
	}
	if utf8.RuneCountInString(name) > MaxProviderNameLength {
		return fmt.Errorf("provider name is longer than %d characters", MaxProviderNameLength)
	}
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
		case r == ' ', r == '.', r == '_', r == '-':
		default:
			return fmt.Errorf("provider name contains %q; use letters, digits, spaces and . _ - only", r)
		}
	}
	return nil
}

// renameModelRef rewrites "old/model" to "new/model". Bare model ids and
// refs naming another provider are returned unchanged. Matching is exact
// (case-sensitive), the same rule the runtime uses to resolve the prefix.
func renameModelRef(ref, oldName, newName string) (string, bool) {
	trimmed := strings.TrimSpace(ref)
	prefix := oldName + "/"
	if !strings.HasPrefix(trimmed, prefix) || len(trimmed) == len(prefix) {
		return ref, false
	}
	return newName + "/" + trimmed[len(prefix):], true
}

func renameModelRefList(refs []string, oldName, newName string) bool {
	changed := false
	for i := range refs {
		if next, ok := renameModelRef(refs[i], oldName, newName); ok {
			refs[i] = next
			changed = true
		}
	}
	return changed
}

// RenameProviderRefs rewrites every reference to provider oldName in one
// config layer: the [[providers]] entry itself (the API key lives in that
// entry, so it moves with it), default_provider, every "provider/model"
// setting (task/advisor/review/orchestrator/compact/darwin judge, fallback
// and darwin model lists, the council roster), provider-scoped manual
// prices and media-generation key bindings. hidden_models uses an encoded
// form owned by the providers package and is handled there. It reports
// whether anything changed so callers can skip writing untouched layers.
func RenameProviderRefs(tc *TomlConfig, oldName, newName string) bool {
	if tc == nil || oldName == "" || newName == "" || oldName == newName {
		return false
	}
	changed := false
	for i := range tc.Providers {
		if tc.Providers[i].Name == oldName {
			tc.Providers[i].Name = newName
			changed = true
		}
	}
	if tc.DefaultProvider == oldName {
		tc.DefaultProvider = newName
		changed = true
	}
	for _, field := range []*string{
		&tc.TaskModel, &tc.AdvisorModel, &tc.ReviewModel, &tc.OrchestratorModel,
		&tc.CompactModel, &tc.DarwinJudgeModel,
	} {
		if next, ok := renameModelRef(*field, oldName, newName); ok {
			*field = next
			changed = true
		}
	}
	if renameModelRefList(tc.FallbackModels, oldName, newName) {
		changed = true
	}
	if renameModelRefList(tc.DarwinModels, oldName, newName) {
		changed = true
	}
	if renameModelRefList(tc.Council.Models, oldName, newName) {
		changed = true
	}
	for i := range tc.ModelPrices {
		if tc.ModelPrices[i].Provider == oldName {
			tc.ModelPrices[i].Provider = newName
			changed = true
		}
	}
	for _, media := range []*MediaGenerationProviderConf{tc.MediaGeneration.Image, tc.MediaGeneration.Video} {
		if media != nil && media.APIKeyProvider == oldName {
			media.APIKeyProvider = newName
			changed = true
		}
	}
	return changed
}

// RenameProvider moves every provider-scoped context budget from oldName to
// newName. Keys compare case-insensitively, matching Get/Set. An existing
// budget under newName (left over from an earlier provider) is replaced.
func (s *ModelContextStore) RenameProvider(oldName, newName string) (int, error) {
	if s == nil {
		return 0, nil
	}
	oldName, newName = strings.TrimSpace(oldName), strings.TrimSpace(newName)
	if oldName == "" || newName == "" || oldName == newName {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	moved := 0
	for key, entry := range s.entries {
		if !strings.EqualFold(entry.Provider, oldName) {
			continue
		}
		delete(s.entries, key)
		entry.Provider = newName
		s.entries[modelContextKey(newName, entry.Model)] = entry
		moved++
	}
	if moved == 0 {
		return 0, nil
	}
	return moved, s.saveLocked()
}
