package skills

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
)

const (
	defaultSkillResourceLines   = 300
	maxSkillResourceLineBytes   = 1800
	maxSkillResourceOutputBytes = 64 << 10
)

// Resource reads use the selected skill's own directory as a strict boundary.
// They do not change activation state, replay guidance, or execute resources.
func (s *SkillApplier) readResource(ctx context.Context, name, resource string, fromArg, toArg *int) (Result, error) {
	if name == "" {
		return Result{Err: fmt.Errorf("apply_skill: resource requires an exact skill name")}, nil
	}
	rel := strings.ReplaceAll(resource, "\\", "/")
	if rel == "" || strings.HasPrefix(rel, "/") || filepath.IsAbs(resource) || filepath.VolumeName(resource) != "" || strings.Contains(rel, ":") {
		return Result{Err: fmt.Errorf("apply_skill: resource must be a relative path inside the skill folder")}, nil
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return Result{Err: fmt.Errorf("apply_skill: resource may not contain a parent-directory component")}, nil
		}
	}
	s.mu.Lock()
	detail, cached := s.details[name]
	s.mu.Unlock()
	root := filepath.Dir(detail.path)
	if !cached {
		var err error
		root, err = s.discoverer.resourceDir(name)
		if err != nil {
			return Result{Err: fmt.Errorf("apply_skill: resource: %w", err)}, nil
		}
	}
	full, err := sandbox.ResolveWithin(root, filepath.FromSlash(rel))
	if err != nil {
		return Result{Err: fmt.Errorf("apply_skill: resource is outside the selected skill folder: %w", err)}, nil
	}
	from := 1
	if fromArg != nil {
		from = *fromArg
	}
	to := math.MaxInt
	if from <= math.MaxInt-defaultSkillResourceLines+1 {
		to = from + defaultSkillResourceLines - 1
	}
	if toArg != nil {
		to = *toArg
	}
	if from < 1 || to < from {
		return Result{Err: fmt.Errorf("apply_skill: resource lines must satisfy 1 <= from <= to")}, nil
	}
	requestedTo := to
	if to >= from && to-from >= fileops.MaxLineRange {
		to = from + fileops.MaxLineRange - 1
	}
	if info, err := os.Stat(full); err != nil {
		return Result{Err: fmt.Errorf("apply_skill: resource: %w", err)}, nil
	} else if !info.Mode().IsRegular() {
		return Result{Err: fmt.Errorf("apply_skill: resource must be a regular text file")}, nil
	}
	lines, eof, err := fileops.ReadLinesBoundedWithEOF(ctx, full, from, to, maxSkillResourceLineBytes)
	if err != nil {
		return Result{Err: fmt.Errorf("apply_skill: resource: %w", err)}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "skill_resource name=%q resource=%q\n", name, resource)
	continuationBytes := len(fmt.Sprintf("[continue with apply_skill {\"name\":%q,\"resource\":%q,\"from\":%d}]\n", name, resource, math.MaxInt))
	outputBudget := maxSkillResourceOutputBytes - continuationBytes - 256
	if b.Len() > outputBudget {
		return Result{Err: fmt.Errorf("apply_skill: resource identifiers exceed the output budget")}, nil
	}
	next := to
	if next < math.MaxInt {
		next++
	}
	for _, line := range lines {
		// The shared reader marks long lines; avoid suggesting shell execution for
		// a resource whose read permission does not grant executable access.
		content := line.Content
		if len(content) > maxSkillResourceLineBytes && strings.HasSuffix(content, "; use ctx_execute for the full line]") {
			content = strings.TrimSuffix(content, "; use ctx_execute for the full line]") + "]"
		}
		row := fmt.Sprintf("%4d | %s\n", line.Number, content)
		if b.Len()+len(row) > outputBudget {
			fmt.Fprintf(&b, "[output capped at %d KiB; continue from line %d]\n", maxSkillResourceOutputBytes/1024, line.Number)
			next, eof = line.Number, false
			break
		}
		b.WriteString(row)
	}
	if eof && len(lines) > 0 {
		fmt.Fprintf(&b, "[end of resource at line %d]\n", lines[len(lines)-1].Number)
	} else if !eof {
		if to < requestedTo {
			fmt.Fprintf(&b, "[range capped at %d lines]\n", fileops.MaxLineRange)
		}
		fmt.Fprintf(&b, "[continue with apply_skill {\"name\":%q,\"resource\":%q,\"from\":%d}]\n", name, resource, next)
	}
	return Result{Text: b.String()}, nil
}

// Resolve metadata/materialization without opening SKILL.md again. This also
// supports resource reads after an applier is rebuilt for a resumed session.
func (d *Discoverer) resourceDir(name string) (string, error) {
	catalog, err := d.catalog()
	if err != nil {
		return "", err
	}
	for _, skill := range catalog {
		if skill.Name != name {
			continue
		}
		if strings.HasPrefix(skill.Path, builtinScheme) && d.Builtin != nil {
			return d.Builtin.resourceDir(name)
		}
		return filepath.Dir(skill.Path), nil
	}
	return "", fmt.Errorf("skill %q not found", name)
}

func (p *BuiltinPack) resourceDir(name string) (string, error) {
	if err := p.ensure(); err != nil {
		return "", err
	}
	entry, ok := p.meta[name]
	if !ok {
		return "", fmt.Errorf("skill %q not found in builtin pack", name)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.materializeLocked(entry)
}
