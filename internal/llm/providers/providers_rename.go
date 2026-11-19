package providers

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// fileSnapshot remembers a config file's exact bytes so a failed multi-file
// rename can be undone without a lossy TOML round-trip.
type fileSnapshot struct {
	path   string
	data   []byte
	exists bool
}

func snapshotFile(path string) (fileSnapshot, error) {
	snap := fileSnapshot{path: path}
	data, err := os.ReadFile(path)
	if err == nil {
		snap.data, snap.exists = data, true
		return snap, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return snap, nil
	}
	return snap, err
}

func (s fileSnapshot) restore() error {
	if !s.exists {
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return os.WriteFile(s.path, s.data, 0o600)
}

// Rename changes a configured provider's name and rewrites every reference
// to it in the global config and, when a project config is active, in that
// layer too (see config.RenameProviderRefs), plus the provider-scoped
// hidden-model state. The API key is stored inside the provider entry, so it
// moves with the entry and is never copied, logged or returned.
//
// Renaming to the same name is a no-op. The new name must pass
// config.ValidateProviderName and must not collide (case-insensitively) with
// another provider. On any write failure every touched file is restored to
// its previous bytes and the in-memory state is rolled back.
//
// Session rows, context budgets and live capability entries live outside
// config.toml; callers update them via RenameProviderEverywhere.
func (m *Manager) Rename(oldName, newName string) error {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" {
		return errors.New("provider name is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := -1
	for i, p := range m.providers {
		if p.Name == oldName {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("provider %q not found", oldName)
	}
	if oldName == newName {
		return nil
	}
	if err := config.ValidateProviderName(newName); err != nil {
		return err
	}
	for i, p := range m.providers {
		if i != idx && strings.EqualFold(p.Name, newName) {
			return fmt.Errorf("provider %q already exists", p.Name)
		}
	}

	paths := []string{m.tomlPath}
	if m.activePath != "" && m.activePath != m.tomlPath {
		paths = append(paths, m.activePath)
	}
	snaps := make([]fileSnapshot, 0, len(paths))
	for _, path := range paths {
		snap, err := snapshotFile(path)
		if err != nil {
			return fmt.Errorf("rename provider: read %s: %w", path, err)
		}
		snaps = append(snaps, snap)
	}
	prevProviders := append([]config.ProviderConf(nil), m.providers...)
	prevHidden := make(map[string]struct{}, len(m.hidden))
	for key := range m.hidden {
		prevHidden[key] = struct{}{}
	}
	rollback := func(cause error) error {
		m.providers = prevProviders
		m.hidden = prevHidden
		var restoreErrs []error
		for _, snap := range snaps {
			if err := snap.restore(); err != nil {
				restoreErrs = append(restoreErrs, err)
			}
		}
		if len(restoreErrs) > 0 {
			return fmt.Errorf("rename provider: %w; restoring config failed: %v", cause, errors.Join(restoreErrs...))
		}
		return fmt.Errorf("rename provider: %w", cause)
	}

	m.providers = append([]config.ProviderConf(nil), m.providers...)
	m.providers[idx].Name = newName
	renamedHidden := make(map[string]struct{}, len(m.hidden))
	for key := range m.hidden {
		if provider, model, ok := strings.Cut(key, "\x00"); ok && provider == oldName {
			key = hiddenKey(newName, model)
		}
		renamedHidden[key] = struct{}{}
	}
	m.hidden = renamedHidden

	// Global layer: providers list (authoritative in memory), references
	// and the hidden set, written once.
	global, err := config.LoadToml(m.tomlPath)
	if err != nil {
		return rollback(err)
	}
	global.Providers = m.providers
	config.RenameProviderRefs(&global, oldName, newName)
	// Rewrite the persisted hidden refs from the file itself, not from
	// m.hidden: a manager that never ran LoadHiddenState must not wipe them.
	for i, raw := range global.HiddenModels {
		if key, ok := decodeHiddenRef(raw); ok {
			if provider, model, cut := strings.Cut(key, "\x00"); cut && provider == oldName {
				global.HiddenModels[i] = encodeHiddenRef(hiddenKey(newName, model))
			}
		}
	}
	if err := config.SaveToml(m.tomlPath, global); err != nil {
		return rollback(err)
	}
	// Project layer: only references (and a copied provider entry, if
	// SaveActiveConfig placed one there). Untouched layers are not rewritten.
	if len(paths) > 1 {
		project, err := config.LoadToml(m.activePath)
		if err != nil {
			return rollback(err)
		}
		if config.RenameProviderRefs(&project, oldName, newName) {
			if err := config.SaveToml(m.activePath, project); err != nil {
				return rollback(err)
			}
		}
	}
	return nil
}

// RelabelCapabilities moves live registry entries discovered for oldName to
// newName, so pickers keep listing the provider's models without a rescan.
func RelabelCapabilities(caps *llm.CapabilityRegistry, oldName, newName string) int {
	if caps == nil || oldName == "" || newName == "" || oldName == newName {
		return 0
	}
	moved := 0
	for _, mi := range caps.All() {
		if mi.Provider == oldName {
			// Register replaces in place; the entry keeps its own Source.
			mi.Provider = newName
			caps.Register(mi)
			moved++
		}
	}
	return moved
}

// SessionProviderRenamer is the session-store capability needed to keep
// saved sessions pointing at a renamed provider.
type SessionProviderRenamer interface {
	RenameProvider(oldName, newName string) (int64, error)
}

// ContextBudgetRenamer is the context-budget store capability (config.
// ModelContextStore) needed to keep per-provider budgets after a rename.
type ContextBudgetRenamer interface {
	RenameProvider(oldName, newName string) (int, error)
}

// RenameProviderEverywhere renames a provider in config (Manager.Rename) and
// then in the stores that key on the provider name: saved sessions, context
// budgets and the live capability registry. Any nil dependency is skipped.
// If the session store fails, the config rename is reverted so the two never
// disagree. A context-budget failure is reported but not rolled back: the
// budgets are an optional cache of user overrides.
func RenameProviderEverywhere(m *Manager, oldName, newName string, sessions SessionProviderRenamer, budgets ContextBudgetRenamer, caps *llm.CapabilityRegistry) error {
	if m == nil {
		return errors.New("provider manager is unavailable")
	}
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if err := m.Rename(oldName, newName); err != nil {
		return err
	}
	if oldName == newName {
		return nil
	}
	if sessions != nil {
		if _, err := sessions.RenameProvider(oldName, newName); err != nil {
			if undoErr := m.Rename(newName, oldName); undoErr != nil {
				return fmt.Errorf("rename provider: update sessions: %v; reverting config failed: %w", err, undoErr)
			}
			return fmt.Errorf("rename provider: update sessions: %w", err)
		}
	}
	RelabelCapabilities(caps, oldName, newName)
	if budgets != nil {
		if _, err := budgets.RenameProvider(oldName, newName); err != nil {
			return fmt.Errorf("provider renamed, but context budgets were not moved: %w", err)
		}
	}
	return nil
}
