package skills

import (
	"archive/zip"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	builtinScheme   = "builtin://"
	maxSkillExtract = int64(64 << 20)
)

// The executable carries only the compact metadata index. The complete
// skills-database archive is shared by TUI and WebGUI from the portable data
// directory: supercli-data/skills/builtin-skills.zip.
//
//go:embed builtin_skills_index.json
var builtinSkillsIndex []byte

type builtinIndexEntry struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Category    string `json:"category"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Risk        string `json:"risk"`
	Source      string `json:"source"`
}

// BuiltinPack is a lazy view over the embedded skill archive. Index parsing
// happens once; individual directories are decompressed only when applied.
type BuiltinPack struct {
	dataDir string
	once    sync.Once
	err     error
	meta    map[string]builtinIndexEntry
	catalog []Skill
	mu      sync.Mutex
}

func NewBuiltinPack(dataDir string) *BuiltinPack {
	return &BuiltinPack{dataDir: dataDir}
}

func (p *BuiltinPack) ensure() error {
	if p == nil {
		return fmt.Errorf("builtin skill pack is nil")
	}
	p.once.Do(func() {
		var entries []builtinIndexEntry
		if err := json.Unmarshal(builtinSkillsIndex, &entries); err != nil {
			p.err = fmt.Errorf("parse embedded skill index: %w", err)
			return
		}
		p.meta = make(map[string]builtinIndexEntry, len(entries))
		p.catalog = make([]Skill, 0, len(entries))
		for _, entry := range entries {
			entry.Name = strings.TrimSpace(entry.Name)
			if entry.Name == "" {
				entry.Name = strings.TrimSpace(entry.ID)
			}
			entry.Path = strings.Trim(strings.TrimSpace(filepath.ToSlash(entry.Path)), "/")
			if entry.Name == "" || entry.Path == "" {
				continue
			}
			if _, exists := p.meta[entry.Name]; exists {
				continue
			}
			p.meta[entry.Name] = entry
			p.catalog = append(p.catalog, Skill{
				Name: entry.Name, Path: builtinScheme + entry.Path + "/SKILL.md",
				Description: entry.Description, Category: entry.Category,
				Risk: entry.Risk, Source: entry.Source, Priority: 10,
			})
		}
	})
	return p.err
}

func (p *BuiltinPack) Catalog() ([]Skill, error) {
	if err := p.ensure(); err != nil {
		return nil, err
	}
	out := make([]Skill, len(p.catalog))
	copy(out, p.catalog)
	return out, nil
}

// Load materializes one selected skill into the portable cache and reads its
// SKILL.md. Other skills remain compressed in the shared data archive.
func (p *BuiltinPack) Load(name string) (*Skill, error) {
	if err := p.ensure(); err != nil {
		return nil, err
	}
	entry, ok := p.meta[name]
	if !ok {
		return nil, fmt.Errorf("skill %q not found in builtin pack", name)
	}
	p.mu.Lock()
	dir, err := p.materializeLocked(entry)
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	s, err := readSkill(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return nil, err
	}
	s.Name = entry.Name
	s.Description = entry.Description
	s.Category = entry.Category
	s.Risk = entry.Risk
	s.Source = entry.Source
	s.Priority = 10
	return &s, nil
}

func (p *BuiltinPack) materializeLocked(entry builtinIndexEntry) (string, error) {
	if strings.TrimSpace(p.dataDir) == "" {
		return "", fmt.Errorf("materialize builtin skill: data directory is empty")
	}
	archivePath := filepath.Join(p.dataDir, "skills", "builtin-skills.zip")
	info, err := os.Stat(archivePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("builtin skill pack is missing at %s; place builtin-skills.zip in supercli-data/skills", archivePath)
		}
		return "", fmt.Errorf("stat builtin skill pack: %w", err)
	}
	// Size + mtime gives each replaced archive a fresh cache namespace. No
	// stale resources survive an update, and hashing 32 MB at startup is avoided.
	signature := fmt.Sprintf("%x-%x", info.Size(), info.ModTime().UnixNano())
	root := filepath.Join(p.dataDir, "cache", "builtin-skills", signature)
	dir := filepath.Join(root, filepath.FromSlash(entry.Path))
	marker := filepath.Join(dir, ".complete")
	if _, err := os.Stat(marker); err == nil {
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("materialize skill %q: %w", entry.Name, err)
	}
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("open builtin skill pack %s: %w", archivePath, err)
	}
	defer zr.Close()
	prefix := strings.Trim(entry.Path, "/") + "/"
	var total int64
	found := false
	for _, zf := range zr.File {
		name := filepath.ToSlash(zf.Name)
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rel := strings.TrimPrefix(name, prefix)
		if rel == "" {
			continue
		}
		target, err := safeSkillJoin(dir, rel)
		if err != nil {
			return "", err
		}
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
			continue
		}
		if zf.FileInfo().Mode()&os.ModeSymlink != 0 {
			continue
		}
		total += int64(zf.UncompressedSize64)
		if total > maxSkillExtract {
			return "", fmt.Errorf("materialize skill %q: resources exceed %d bytes", entry.Name, maxSkillExtract)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		if err := extractSkillFile(zf, target); err != nil {
			return "", fmt.Errorf("materialize skill %q: %w", entry.Name, err)
		}
		if rel == "SKILL.md" {
			found = true
		}
	}
	if !found {
		return "", fmt.Errorf("builtin skill %q has no SKILL.md", entry.Name)
	}
	if err := os.WriteFile(marker, []byte(signature+"\n"), 0o644); err != nil {
		return "", err
	}
	return dir, nil
}

func safeSkillJoin(root, rel string) (string, error) {
	target := filepath.Join(root, filepath.FromSlash(rel))
	cleanRoot := filepath.Clean(root)
	cleanTarget := filepath.Clean(target)
	back, err := filepath.Rel(cleanRoot, cleanTarget)
	if err != nil || back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe path %q in builtin skill pack", rel)
	}
	return cleanTarget, nil
}

func extractSkillFile(zf *zip.File, target string) error {
	r, err := zf.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
