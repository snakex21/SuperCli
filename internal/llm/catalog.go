package llm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// catalogFileName is the on-disk name of the user
// catalog. Living in the SuperCli data directory
// (portable: supercli-data next to the binary), the
// file is plain UTF-8 JSON, editable by hand, and
// re-loaded on every SuperCli start.
const catalogFileName = "models.json"

// CatalogPath returns the on-disk path of the user
// catalog: <dataDir>/models.json.
func CatalogPath(dataDir string) string {
	return filepath.Join(dataDir, catalogFileName)
}

// LoadCatalog reads <dataDir>/models.json.
//
// A missing file is NOT an error: the user has not
// created a catalog yet, so we return an empty
// slice. A malformed file IS an error: silently
// dropping corrupted data would let the user
// lose their edits without notice.
//
// Entries with an empty ID are skipped. The Source
// field of every loaded entry is forced to
// SourceCatalog, because we are reading from the
// user-edited file. The result is sorted by id so
// callers can rely on stable ordering.
func LoadCatalog(home string) ([]ModelInfo, error) {
	path := CatalogPath(home)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return parseCatalogBytes(data)
}

func parseCatalogBytes(data []byte) ([]ModelInfo, error) {
	list := []json.RawMessage{}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	out := make([]ModelInfo, 0, len(list))
	for _, raw := range list {
		var m ModelInfo
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		if m.ID == "" {
			continue
		}
		if m.Source == SourceUnknown {
			m.Source = SourceCatalog
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// SaveCatalog writes the entries to
// <home>/.supercli/models.json. The Source field
// is stripped on the way out: it is in-memory
// provenance, not user-facing data. The parent
// directory is created if it does not exist. The
// write is atomic: we marshal, write to a sibling
// <path>.tmp, then os.Rename onto the real path.
// On Windows os.Rename uses MoveFileEx with
// MOVEFILE_REPLACE_EXISTING, so the rename is
// atomic on every platform SuperCli supports.
//
// The result is sorted by id so the file diff is
// minimal across runs.
func SaveCatalog(home string, models []ModelInfo) error {
	path := CatalogPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	cleaned := make([]ModelInfo, 0, len(models))
	for _, m := range models {
		m.Source = SourceUnknown
		cleaned = append(cleaned, m)
	}
	sort.Slice(cleaned, func(i, j int) bool { return cleaned[i].ID < cleaned[j].ID })
	data, err := json.MarshalIndent(cleaned, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// MergeCatalog upserts the fresh entries into the
// existing entries. Three rules govern conflicts:
//
//  1. Source.Overrides wins. A higher-priority
//     source (Probe > Provider > Catalog > Seed
//     > Unknown) replaces a lower-priority one.
//     This is the cardinal F16 rule: the Seed
//     NEVER overwrites the user's Catalog.
//
//  2. Same Source — the freshest LastVerified
//     wins. If the existing entry's LastVerified
//     is strictly after the fresh entry's, we
//     keep the existing one. A zero LastVerified
//     loses to any non-zero LastVerified, so
//     newly-loaded (un-dated) entries cannot
//     silently stomp on entries the user has
//     touched.
//
//  3. Unknown id is always added.
//
// The result is sorted by id. Neither input is
// mutated; the function is pure.
func MergeCatalog(existing, fresh []ModelInfo) []ModelInfo {
	byID := make(map[string]ModelInfo, len(existing))
	for _, m := range existing {
		byID[m.ID] = m
	}
	for _, m := range fresh {
		cur, ok := byID[m.ID]
		if !ok {
			byID[m.ID] = m
			continue
		}
		if !m.Source.Overrides(cur.Source) {
			// Strictly lower source — keep existing.
			continue
		}
		if m.Source == cur.Source {
			// Same source — freshest LastVerified wins.
			// A zero LastVerified loses to any non-zero.
			if cur.LastVerified.After(m.LastVerified) {
				continue
			}
		}
		byID[m.ID] = m
	}
	out := make([]ModelInfo, 0, len(byID))
	for _, m := range byID {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
