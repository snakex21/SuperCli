package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// SkillApplier activates a skill by name, appending its
// content to the running system prompt. It is exposed to the
// model as the `apply_skill` meta-tool. Multiple activations
// are deduped by name and order is preserved.
type SkillApplier struct {
	mu         sync.Mutex
	discoverer *Discoverer
	applied    map[string]struct{} // dedupe
	order      []string
	preamble   string // separator inserted between skills
}

// NewSkillApplier builds a SkillApplier. The preamble is
// prepended to each skill block in the system prompt; the
// default is two newlines.
func NewSkillApplier(d *Discoverer) *SkillApplier {
	return &SkillApplier{
		discoverer: d,
		applied:    make(map[string]struct{}),
		preamble:   "\n\n",
	}
}

// Spec returns the meta-tool description. Always-on: the
// model can call it in any turn.
func (s *SkillApplier) Spec() Tool {
	return Tool{
		Name: "apply_skill",
		Description: "Activate a discovered skill by name. The skill " +
			"content is appended to the system prompt for the " +
			"current session. List available skills with " +
			"`tool_search` (query='skill') or the /skills TUI " +
			"command. Idempotent: re-applying a skill is a no-op.",
		Schema: `{
			"type": "object",
			"properties": {
				"name": {"type": "string", "description": "skill name, e.g. 'code-review' or 'supercli-dev'"}
			},
			"required": ["name"]
		}`,
		Fn: s.execute,
	}
}

// applyArgs is the JSON shape the model sends.
type applyArgs struct {
	Name string `json:"name"`
}

// execute resolves the skill, marks it applied, and returns
// a short confirmation. The system-prompt mutation is done
// in AppendSkills which the loop calls after execute.
func (s *SkillApplier) execute(_ context.Context, args json.RawMessage) (Result, error) {
	var a applyArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("apply_skill: bad args: %w", err)}, nil
	}
	name := strings.TrimSpace(a.Name)
	if name == "" {
		return Result{Err: fmt.Errorf("apply_skill: name is empty")}, nil
	}
	skill, err := s.discoverer.Get(name)
	if err != nil {
		return Result{Err: fmt.Errorf("apply_skill: %w", err)}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.applied[skill.Name]; ok {
		return Result{Text: fmt.Sprintf("skill %q already applied (no-op)", skill.Name)}, nil
	}
	s.applied[skill.Name] = struct{}{}
	s.order = append(s.order, skill.Name)
	return Result{Text: fmt.Sprintf("skill %q applied (%d bytes of guidance)", skill.Name, len(skill.Content))}, nil
}

// AppendSkills returns the joined content of all applied
// skills, in the order they were applied. The loop prepends
// this to the system prompt. If no skills are applied, the
// returned string is empty.
func (s *SkillApplier) AppendSkills() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.order) == 0 {
		return ""
	}
	var b strings.Builder
	for _, n := range s.order {
		skill, err := s.discoverer.Get(n)
		if err != nil {
			continue
		}
		b.WriteString(s.preamble)
		b.WriteString("## Skill: ")
		b.WriteString(skill.Name)
		b.WriteString("\n\n")
		b.WriteString(skill.Content)
	}
	return b.String()
}

// Applied returns the names of applied skills, in order.
// Useful for tests and the /skills TUI command.
func (s *SkillApplier) Applied() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.order))
	copy(out, s.order)
	return out
}

// Reset clears the applied set. The loop calls this between
// sessions (each new session starts with no skills).
func (s *SkillApplier) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applied = make(map[string]struct{})
	s.order = nil
}
