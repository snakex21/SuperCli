package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Completion check: a would-be final answer that itself says part of the
// request is not done ("I did not finish", "I haven't changed any files",
// "nie udało się") gets ONE more pass per Run: do every remaining part that
// is possible and report only what is truly blocked, instead of giving up on
// the whole request. An answer that already reports a missing target after
// other edits in the same Run is the honest partial result and is not
// re-asked. The check costs nothing on any other Run; config
// completion_check = false turns it off.
//
// 1.0.27 release candidate 1 also re-asked every Run that edited 3+ files to
// search for leftovers. Its ablation on the v2 suite (3 repeats each) was
// 75.9 +/- 3.2 % with it vs 70.4 +/- 8.5 % without, inside the noise, for +15 %
// cost, so that trigger was dropped; the Core prompt line asking for a
// leftovers search after multi-file work remains.

type completionCheckState struct {
	mu    sync.Mutex
	files map[string]struct{}
	fired bool
}

func (l *Loop) resetCompletionCheck() {
	l.completion.mu.Lock()
	l.completion.files = nil
	l.completion.fired = false
	l.completion.mu.Unlock()
}

// noteCompletionMutation records the files a successful, non-inert mutation
// changed. Tool batches may run concurrently, hence the mutex.
func (l *Loop) noteCompletionMutation(name, args string, res tools.Result) {
	if !l.completionCheck {
		return
	}
	paths := mutationPaths(name, args, res)
	if len(paths) == 0 {
		return
	}
	l.completion.mu.Lock()
	defer l.completion.mu.Unlock()
	if l.completion.files == nil {
		l.completion.files = map[string]struct{}{}
	}
	for _, p := range paths {
		key := filepath.Clean(filepath.FromSlash(strings.TrimSpace(p)))
		if filepath.IsAbs(key) && l.baseDir != "" {
			if rel, err := filepath.Rel(l.baseDir, key); err == nil && !strings.HasPrefix(rel, "..") {
				key = rel
			}
		}
		l.completion.files[strings.ToLower(key)] = struct{}{}
	}
}

func mutationPaths(name, args string, res tools.Result) []string {
	if len(res.ChangedPaths) > 0 {
		return res.ChangedPaths
	}
	var a struct {
		Path string `json:"path"`
		Dest string `json:"dest"`
	}
	if json.Unmarshal([]byte(args), &a) != nil {
		return nil
	}
	switch name {
	case "patch_file", "create_file", "write_file", "edit_docx", "edit_xlsx":
		if a.Path != "" {
			return []string{a.Path}
		}
	case "move", "copy":
		if a.Dest != "" {
			return []string{a.Dest}
		}
	}
	return nil
}

// unfinishedReply matches a final answer that admits part of the requested
// work was not done. English and Polish (the product's two main languages);
// a miss only means no extra pass.
var unfinishedReply = regexp.MustCompile(`(?i)` +
	`\b(?:did not|didn't|could not|couldn't|cannot|can't|can not|was not able to|wasn't able to|unable to|have not|haven't|has not|hasn't)\s+(?:yet\s+|safely\s+|fully\s+)?(?:finish|complete|make|made|apply|applied|do|done|edit|update|change|add|process|modify)\w*` +
	`|\b(?:task|work|change|edit|update)s?\s+(?:is|are|remains?)\s+(?:still\s+)?(?:incomplete|unfinished|partial)\b` +
	`|\bstill\s+(?:need|needs|missing|lack|lacks|remain|remains|left)\b|\bremaining\s+(?:files|items|steps|work|changes)\b` +
	`|\bnie\s+(?:udało\s+mi\s+się|udało\s+się|zdołałe[mś]|zdołałam|dokończył\w*|wprowadził\w*|zmienił\w*|wykonał\w*)` +
	`|\bniedokończon\w*|\bpozostał\w*\s+do\s+(?:zrobienia|zmiany|poprawienia)`)

// missingTargetReply matches an answer that names a missing file or target as
// the reason something was not done. After other edits in the same Run that
// is the honest partial result the check asks for, so it is not re-asked.
var missingTargetReply = regexp.MustCompile(`(?i)\b(?:does not exist|doesn't exist|did not exist|didn't exist|not found|no such|is missing|are missing|missing from|nie istnieje|nie ma (?:takiego|pliku)|brak(?:uje)?)\b`)

// runCompletionCheck appends the check instruction and returns true when the
// Run must continue; false lets it finish as usual.
func (l *Loop) runCompletionCheck(ctx context.Context, out chan<- Event, text string, canContinue bool) bool {
	if !canContinue || ctx.Err() != nil {
		return false
	}
	l.completion.mu.Lock()
	fired, files := l.completion.fired, len(l.completion.files)
	l.completion.mu.Unlock()
	if fired {
		return false
	}
	_, haveEditFiles := l.registry.Get("edit_files")
	manyFiles := ""
	if haveEditFiles {
		manyFiles = " (edit_files applies one change to many files)"
	}
	// Models often write typographic apostrophes (couldn’t, didn’t).
	text = strings.ReplaceAll(text, "’", "'")
	if !unfinishedReply.MatchString(text) || (files > 0 && missingTargetReply.MatchString(text)) {
		return false
	}
	content := "[completion check] Your reply says part of the request is not done. " +
		"Do every remaining part that is possible now" + manyFiles + "; one missing or impossible part never blocks the others. " +
		"Never create, guess or substitute a missing file or target. " +
		"Then give the final answer: what you changed, and exactly what could not be done and why."
	notice := "completion check: the reply admits unfinished work; asking to finish the possible parts"
	l.completion.mu.Lock()
	l.completion.fired = true
	l.completion.mu.Unlock()
	msg := llm.Message{Role: llm.RoleSystem, Content: content}
	l.Messages = append(l.Messages, msg)
	l.persist(ctx, msg)
	if out != nil {
		out <- NoticeEvent{Text: notice}
	}
	return true
}
