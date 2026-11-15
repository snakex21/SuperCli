package agent

import (
	"context"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"supercli/internal/llm"
)

// Scope check: a request that limits the work to what it lists ("make these
// changes and nothing else", "only these files", "nic więcej") and names the
// files involved gets ONE review pass per Run when the Run changed other
// files. The model keeps such a change only if a listed step cannot work
// without it, and otherwise restores the file; a step that cannot be done as
// written is reported, never worked around elsewhere. This is the diff-side
// half of "verify before use": the name check stops a missing name from
// being invented up front, the scope check catches the workaround (editing a
// look-alike, then fixing the tests it broke) before the Run reports success.
// No model call unless it fires; config scope_check = false turns it off.

var scopeLimited = regexp.MustCompile(`(?i)(?:\bnothing else\b|\band nothing more\b|\bno other (?:changes?|files?|edits?)\b|\b(?:do not|don't|never) (?:change|touch|modify|edit) (?:anything|any other)\b|\bonly (?:these|the listed|the following)\b|\bexactly these\b|\bnic (?:więcej|innego)|\bniczego (?:więcej|innego)|\btylko (?:te|to|tych|w tych)\b|\bwyłącznie)`)

// scopeExtraFiles returns the files this Run changed that a scope-limited
// prompt does not name. Nil when the prompt is not scope-limited or names no
// file at all (then there is nothing to compare against).
func scopeExtraFiles(prompt string, changed []string) []string {
	if !scopeLimited.MatchString(prompt) {
		return nil
	}
	named := map[string]bool{}
	for _, c := range requestPathMentions(prompt) {
		named[strings.ToLower(filepath.ToSlash(c))] = true
		named[strings.ToLower(filepath.Base(filepath.FromSlash(c)))] = true
	}
	if len(named) == 0 {
		return nil
	}
	var extra []string
	for _, f := range changed {
		rel := strings.ToLower(filepath.ToSlash(f))
		if named[rel] || named[strings.ToLower(filepath.Base(filepath.FromSlash(f)))] {
			continue
		}
		extra = append(extra, filepath.ToSlash(f))
	}
	sort.Strings(extra)
	return extra
}

// requestPathMentions lists the file paths a prompt names (with an extension).
func requestPathMentions(prompt string) []string {
	var out []string
	for _, m := range nameCheckPath.FindAllStringSubmatch(" "+prompt, -1) {
		out = append(out, m[1])
	}
	for _, m := range nameCheckBacktick.FindAllStringSubmatch(prompt, -1) {
		if c, ok := nameCheckParse(m[1], true); ok && c.path {
			out = append(out, c.name)
		}
	}
	return out
}

// runScopeCheck appends the scope review and returns true when the Run must
// continue.
func (l *Loop) runScopeCheck(ctx context.Context, out chan<- Event, canContinue bool) bool {
	if !canContinue || ctx.Err() != nil {
		return false
	}
	l.completion.mu.Lock()
	if l.completion.scopeFired {
		l.completion.mu.Unlock()
		return false
	}
	changed := make([]string, 0, len(l.completion.files))
	for f := range l.completion.files {
		changed = append(changed, f)
	}
	prompt := l.completion.prompt
	l.completion.mu.Unlock()
	extra := scopeExtraFiles(prompt, changed)
	if len(extra) == 0 {
		return false
	}
	l.completion.mu.Lock()
	l.completion.scopeFired = true
	l.completion.mu.Unlock()
	content := "[scope check] The request limits the work to what it lists, but this Run also changed: " + strings.Join(extra, ", ") + ". " +
		"Keep such a change only if a listed step cannot work without it; otherwise restore that file exactly. " +
		"If a step could not be done as written (for example a name that does not exist), do not work around it in other files or by changing tests: leave it undone and report it. " +
		"Then give the final answer: what you changed, and exactly what was not done and why."
	msg := llm.Message{Role: llm.RoleSystem, Content: content}
	l.Messages = append(l.Messages, msg)
	l.persist(ctx, msg)
	if out != nil {
		out <- NoticeEvent{Text: "scope check: files outside the request's stated scope were changed; asking to review them"}
	}
	return true
}
