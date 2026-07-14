// Package scratchpad provides a tiny shared notebook for coordinators and
// workers. Notes live in the workspace, not in model context, so a worker can
// hand over detailed findings without inflating the parent transcript.
package scratchpad

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"supercli/internal/tools/core"
)

const (
	maxNoteBytes  = 32 * 1024
	maxTotalBytes = 512 * 1024
	maxNotes      = 32
)

type Scratchpad struct{ BaseDir string }

func New(baseDir string) *Scratchpad { return &Scratchpad{BaseDir: baseDir} }

func (s *Scratchpad) Spec() Tool {
	return Tool{
		Name: "scratchpad", ReadOnly: false,
		Description: "Share compact notes between coordinator and workers without copying full worker output into chat. Actions: write, read, list.",
		Schema:      `{"type":"object","properties":{"action":{"type":"string","enum":["write","read","list"]},"name":{"type":"string","description":"Short note name"},"text":{"type":"string","description":"Content for write"}},"required":["action"]}`,
		Fn:          s.run,
	}
}

type args struct{ Action, Name, Text string }

func (s *Scratchpad) run(_ context.Context, raw json.RawMessage) (Result, error) {
	var a args
	if err := json.Unmarshal(raw, &a); err != nil {
		return Result{Err: fmt.Errorf("scratchpad: bad args: %w", err)}, nil
	}
	root := filepath.Join(s.BaseDir, ".supercli", "scratchpad")
	switch strings.ToLower(strings.TrimSpace(a.Action)) {
	case "list":
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			return Result{Text: "scratchpad empty"}, nil
		}
		if err != nil {
			return Result{Err: err}, nil
		}
		names := []string{}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				names = append(names, strings.TrimSuffix(e.Name(), ".md"))
			}
		}
		sort.Strings(names)
		if len(names) == 0 {
			return Result{Text: "scratchpad empty"}, nil
		}
		return Result{Text: strings.Join(names, "\n")}, nil
	case "read":
		p, err := notePath(root, a.Name)
		if err != nil {
			return Result{Err: err}, nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return Result{Err: fmt.Errorf("scratchpad: %w", err)}, nil
		}
		return Result{Text: core.HeadTail(string(b), 48*1024, 16*1024)}, nil
	case "write":
		p, err := notePath(root, a.Name)
		if err != nil {
			return Result{Err: err}, nil
		}
		text := strings.TrimSpace(a.Text)
		if text == "" {
			return Result{Err: fmt.Errorf("scratchpad: text is empty")}, nil
		}
		if len(text) > maxNoteBytes {
			text = core.HeadTail(text, 48*1024, 16*1024)
		}
		if err := os.MkdirAll(root, 0755); err != nil {
			return Result{Err: err}, nil
		}
		if err := os.WriteFile(p, []byte(text+"\n"), 0644); err != nil {
			return Result{Err: err}, nil
		}
		// Retention runs only on an actual scratchpad write. Ordinary turns do
		// not scan this directory and therefore pay no scratchpad overhead.
		if err := prune(root, p); err != nil {
			return Result{Err: fmt.Errorf("scratchpad: retain: %w", err)}, nil
		}
		return Result{Text: "scratchpad saved: " + filepath.Base(p)}, nil
	default:
		return Result{Err: fmt.Errorf("scratchpad: action must be write, read, or list")}, nil
	}
}

type noteInfo struct {
	path string
	size int64
	mod  int64
}

func prune(root, keep string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	notes := make([]noteInfo, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		n := noteInfo{path: filepath.Join(root, entry.Name()), size: info.Size(), mod: info.ModTime().UnixNano()}
		notes = append(notes, n)
		total += n.size
	}
	sort.Slice(notes, func(i, j int) bool {
		if notes[i].mod == notes[j].mod {
			return notes[i].path < notes[j].path
		}
		return notes[i].mod < notes[j].mod
	})
	for len(notes) > maxNotes || total > maxTotalBytes {
		idx := 0
		if filepath.Clean(notes[idx].path) == filepath.Clean(keep) && len(notes) > 1 {
			idx = 1
		}
		victim := notes[idx]
		if err := os.Remove(victim.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		total -= victim.size
		notes = append(notes[:idx], notes[idx+1:]...)
	}
	return nil
}

func notePath(root, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("scratchpad: name is required")
	}
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("scratchpad: invalid name")
	}
	return filepath.Join(root, b.String()+".md"), nil
}
